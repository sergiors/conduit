package redis

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// SentinelScheme is the Conduit-specific URI scheme that selects Redis
// Sentinel (failover) mode: redis+sentinel://user:password@host:26379?master=name
//
// "redis+sentinel" is a Conduit-defined scheme, not a standard one. It is
// shaped like a regular redis:// URI but names Sentinel nodes instead of data
// nodes:
//
//		redis+sentinel://<user>:<password>@<addr1>[,<addr2>,...][/<db>]?master=<name>
//
//	  - The host section lists one or more comma-separated Sentinel host:port
//	    addresses (bracketed IPv6 literals such as [::1]:26379 are supported).
//	  - The optional userinfo authenticates both the Sentinel nodes and the
//	    Redis master/replicas the Sentinels point at.
//	  - The master query parameter is required and names the Sentinel master
//	    group to monitor; unknown query parameters are rejected.
//	  - The optional path selects the Redis database index (0-15), default 0.
//
// net/url's generic parser accepts the scheme but redis.ParseURL does not, so
// these URIs are handled exclusively by parseSentinelURI in this file. All
// failover behaviour (master discovery, promotion tracking, reconnection) is
// delegated to go-redis's redis.NewFailoverClient — nothing is reimplemented
// here. Errors produced by this file never contain the URI or its credentials.
const SentinelScheme = "redis+sentinel"

// sentinelConfig is the parsed representation of a redis+sentinel:// URI.
type sentinelConfig struct {
	Addrs      []string // sentinel host:port addresses
	Username   string
	Password   string
	MasterName string
	DB         int
}

// isSentinelURI reports whether uri selects Sentinel (failover) mode. The
// check is case-insensitive and ignores surrounding whitespace.
func isSentinelURI(uri string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(uri)), SentinelScheme+"://")
}

// parseSentinelURI parses and validates a redis+sentinel:// URI.
//
// Validation rules:
//   - The URI must parse (net/url) and its scheme must be redis+sentinel
//     (case-insensitive).
//   - At least one Sentinel address is required; the host section may hold
//     comma-separated host:port entries. Every entry must have a non-empty
//     host and a numeric port within 1-65535; bracketed IPv6 entries
//     ([::1]:26379) are allowed. Order is preserved, duplicates are kept.
//   - Userinfo credentials are applied to the Sentinel nodes and the data
//     nodes; userinfo with more than one ':' is rejected as malformed.
//   - The master query parameter is mandatory (whitespace-trimmed); any
//     unknown query parameter is rejected.
//   - The path, when present, must be a Redis DB index in 0-15.
//
// Errors describe the problem and may quote the offending host/port fragment
// (userinfo is stripped by net/url, so fragments cannot carry credentials),
// but never the URI itself or its credentials.
func parseSentinelURI(rawURL string) (sentinelConfig, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil {
		// *url.Error messages embed the full URL, credentials included, so
		// surface only the underlying reason.
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			return sentinelConfig{}, fmt.Errorf("invalid redis+sentinel URI: %w", urlErr.Err)
		}
		return sentinelConfig{}, errors.New("invalid redis+sentinel URI")
	}
	if strings.ToLower(u.Scheme) != SentinelScheme {
		return sentinelConfig{}, fmt.Errorf("unsupported scheme %q: want %q", u.Scheme, SentinelScheme)
	}
	if err := checkRawUserinfo(rawURL); err != nil {
		return sentinelConfig{}, err
	}

	cfg := sentinelConfig{}
	if u.User != nil {
		cfg.Username = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}

	addrs, err := parseSentinelAddrs(u.Host)
	if err != nil {
		return sentinelConfig{}, err
	}
	cfg.Addrs = addrs

	query := u.Query()
	if ms := query["master"]; len(ms) > 1 {
		return sentinelConfig{}, errors.New("master specified multiple times: want a single ?master=<name> query parameter")
	}
	delete(query, "master")
	if len(query) > 0 {
		unknown := make([]string, 0, len(query))
		for name := range query {
			unknown = append(unknown, name)
		}
		slices.Sort(unknown)
		return sentinelConfig{}, fmt.Errorf("unknown query parameter(s) %s: only %q is supported", strings.Join(unknown, ", "), "master")
	}
	cfg.MasterName = strings.TrimSpace(u.Query().Get("master"))
	if cfg.MasterName == "" {
		return sentinelConfig{}, errors.New("missing master name: want a ?master=<name> query parameter naming the Sentinel master")
	}

	if u.Path != "" {
		db, err := parseSentinelDB(u.Path)
		if err != nil {
			return sentinelConfig{}, err
		}
		cfg.DB = db
	}
	return cfg, nil
}

// checkRawUserinfo rejects userinfo containing more than one ':' (for example
// user:pass:word). net/url assigns everything after the first ':' to the
// password and re-escapes colons when round-tripping userinfo, so the check
// must look at the raw URI text; it never includes credentials in its error.
func checkRawUserinfo(rawURL string) error {
	prefix := SentinelScheme + "://"
	if !strings.HasPrefix(strings.ToLower(rawURL), prefix) {
		return nil // scheme is validated elsewhere; nothing to inspect
	}
	authority := rawURL[len(prefix):]
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return nil
	}
	if strings.Count(authority[:at], ":") > 1 {
		return errors.New("malformed userinfo: want at most one ':' between username and password")
	}
	return nil
}

// parseSentinelAddrs splits the URI host section into individual Sentinel
// addresses, validating each one. Surrounding whitespace around an entry is
// tolerated; empty entries are not.
func parseSentinelAddrs(hostList string) ([]string, error) {
	if strings.TrimSpace(hostList) == "" {
		return nil, errors.New("missing sentinel addresses: want at least one host:port")
	}
	addrs := make([]string, 0, strings.Count(hostList, ",")+1)
	for _, part := range strings.Split(hostList, ",") {
		addr, err := parseSentinelAddr(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

// parseSentinelAddr validates a single Sentinel address, which must be a
// host:port pair. Bracketed IPv6 literals ([::1]:26379) are accepted and
// returned in their bracketed form, which is what net.Dial expects.
func parseSentinelAddr(entry string) (string, error) {
	if entry == "" {
		return "", errors.New("empty sentinel address: want host:port")
	}
	if !strings.HasPrefix(entry, "[") {
		host, port, ok := strings.Cut(entry, ":")
		if !ok || strings.ContainsRune(port, ':') {
			return "", fmt.Errorf("invalid sentinel address %q: want host:port", entry)
		}
		if host == "" {
			return "", fmt.Errorf("invalid sentinel address %q: empty host", entry)
		}
		if err := validateSentinelPort(port, entry); err != nil {
			return "", err
		}
		return entry, nil
	}

	closing := strings.Index(entry, "]")
	if closing < 0 {
		return "", fmt.Errorf("invalid sentinel address %q: missing ']'", entry)
	}
	if host := entry[1:closing]; host == "" {
		return "", fmt.Errorf("invalid sentinel address %q: empty host", entry)
	}
	port, ok := strings.CutPrefix(entry[closing+1:], ":")
	if !ok || port == "" {
		return "", fmt.Errorf("invalid sentinel address %q: want [host]:port", entry)
	}
	if err := validateSentinelPort(port, entry); err != nil {
		return "", err
	}
	return entry, nil
}

// validateSentinelPort checks that port is numeric and within 1-65535.
func validateSentinelPort(port, entry string) error {
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("invalid sentinel address %q: port must be numeric", entry)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("invalid sentinel address %q: port %d out of range (want 1-65535)", entry, n)
	}
	return nil
}

// parseSentinelDB parses the optional "/<db>" path component as a Redis DB
// index. An empty path selects DB 0.
func parseSentinelDB(path string) (int, error) {
	if path == "" {
		return 0, nil
	}
	index := strings.TrimPrefix(path, "/")
	db, err := strconv.Atoi(index)
	if err != nil {
		return 0, fmt.Errorf("invalid redis DB index %q in URI path: want an integer 0-15", index)
	}
	if db < 0 || db > 15 {
		return 0, fmt.Errorf("redis DB index %d out of range: want 0-15", db)
	}
	return db, nil
}

// newSentinelClient builds a go-redis failover client that discovers and
// follows the current master through the given Sentinel nodes. All failover
// behaviour is handled by go-redis; connectivity is validated by the caller
// via Ping.
func newSentinelClient(cfg sentinelConfig) *redis.Client {
	return redis.NewFailoverClient(sentinelFailoverOptions(cfg))
}

// sentinelFailoverOptions translates a parsed redis+sentinel URI into
// go-redis failover options.
func sentinelFailoverOptions(cfg sentinelConfig) *redis.FailoverOptions {
	opts := &redis.FailoverOptions{
		MasterName:    cfg.MasterName,
		SentinelAddrs: cfg.Addrs,
		Username:      cfg.Username,
		Password:      cfg.Password,
		DB:            cfg.DB,
	}
	// The Sentinel nodes are separate servers that may require their own
	// credentials: ACL-protected Sentinels need both username and password,
	// while legacy "requirepass" Sentinels need only a password. Reusing the
	// data-node credentials keeps the URI simple and is harmless when the
	// Sentinels have no authentication configured.
	switch {
	case cfg.Username != "":
		opts.SentinelUsername = cfg.Username
		opts.SentinelPassword = cfg.Password
	case cfg.Password != "":
		opts.SentinelPassword = cfg.Password
	}
	return opts
}
