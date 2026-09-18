package redis

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsSentinelURI(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want bool
	}{
		{name: "sentinel scheme", uri: "redis+sentinel://host:26379?master=m", want: true},
		{name: "sentinel scheme case-insensitive", uri: "REDIS+SENTINEL://host:26379?master=m", want: true},
		{name: "surrounding whitespace", uri: "  redis+sentinel://host:26379?master=m ", want: true},
		{name: "plain redis scheme", uri: "redis://host:6379", want: false},
		{name: "secure redis scheme", uri: "rediss://host:6379", want: false},
		{name: "empty", uri: "", want: false},
		{name: "garbage", uri: "not-a-uri at all", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isSentinelURI(tt.uri))
		})
	}
}

func TestParseSentinelURI(t *testing.T) {
	t.Run("valid single address", func(t *testing.T) {
		cfg, err := parseSentinelURI("redis+sentinel://sentinel-1:26379?master=mymaster")
		require.NoError(t, err)
		assert.Equal(t, []string{"sentinel-1:26379"}, cfg.Addrs)
		assert.Equal(t, "mymaster", cfg.MasterName)
		assert.Empty(t, cfg.Username)
		assert.Empty(t, cfg.Password)
		assert.Equal(t, 0, cfg.DB)
	})

	t.Run("multiple addresses keep order", func(t *testing.T) {
		cfg, err := parseSentinelURI("redis+sentinel://sentinel-1:26379,sentinel-2:26379,sentinel-3:26379?master=mymaster")
		require.NoError(t, err)
		assert.Equal(t, []string{"sentinel-1:26379", "sentinel-2:26379", "sentinel-3:26379"}, cfg.Addrs)
	})

	t.Run("username and password", func(t *testing.T) {
		cfg, err := parseSentinelURI("redis+sentinel://user:password@sentinel-1:26379?master=mymaster")
		require.NoError(t, err)
		assert.Equal(t, "user", cfg.Username)
		assert.Equal(t, "password", cfg.Password)
	})

	t.Run("password only", func(t *testing.T) {
		cfg, err := parseSentinelURI("redis+sentinel://:password@sentinel-1:26379?master=m")
		require.NoError(t, err)
		assert.Empty(t, cfg.Username)
		assert.Equal(t, "password", cfg.Password)
	})

	t.Run("username only", func(t *testing.T) {
		cfg, err := parseSentinelURI("redis+sentinel://user@sentinel-1:26379?master=m")
		require.NoError(t, err)
		assert.Equal(t, "user", cfg.Username)
		assert.Empty(t, cfg.Password)
	})

	t.Run("bracketed IPv6 host", func(t *testing.T) {
		cfg, err := parseSentinelURI("redis+sentinel://[::1]:26379?master=m")
		require.NoError(t, err)
		assert.Equal(t, []string{"[::1]:26379"}, cfg.Addrs)
	})

	t.Run("DB path", func(t *testing.T) {
		cfg, err := parseSentinelURI("redis+sentinel://h:26379/2?master=m")
		require.NoError(t, err)
		assert.Equal(t, 2, cfg.DB)
	})

	t.Run("DB path zero", func(t *testing.T) {
		cfg, err := parseSentinelURI("redis+sentinel://h:26379/0?master=m")
		require.NoError(t, err)
		assert.Equal(t, 0, cfg.DB)
	})

	t.Run("master name is trimmed", func(t *testing.T) {
		cfg, err := parseSentinelURI("redis+sentinel://h:26379?master=" + "%20mymaster%20")
		require.NoError(t, err)
		assert.Equal(t, "mymaster", cfg.MasterName)
	})

	t.Run("uppercase scheme is accepted", func(t *testing.T) {
		cfg, err := parseSentinelURI("REDIS+SENTINEL://h:26379?master=m")
		require.NoError(t, err)
		assert.Equal(t, "m", cfg.MasterName)
	})

	t.Run("surrounding whitespace is trimmed", func(t *testing.T) {
		cfg, err := parseSentinelURI("  redis+sentinel://h:26379?master=m  ")
		require.NoError(t, err)
		assert.Equal(t, "m", cfg.MasterName)
	})
}

func TestParseSentinelURIErrors(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want string // substring the error must contain
		hide string // string the error must NOT contain (credentials)
	}{
		{
			name: "missing master query parameter",
			uri:  "redis+sentinel://sentinel-1:26379",
			want: "master",
		},
		{
			name: "empty host with query",
			uri:  "redis+sentinel://?master=m",
			want: "missing sentinel addresses",
		},
		{
			name: "bare scheme, no host or query",
			uri:  "redis+sentinel://",
			want: "missing sentinel addresses",
		},
		{
			name: "entry without port",
			uri:  "redis+sentinel://host1,host2:26379?master=m",
			want: "invalid sentinel address \"host1\"",
		},
		{
			name: "empty host",
			uri:  "redis+sentinel://:26379?master=m",
			want: "empty host",
		},
		{
			name: "non-numeric port",
			uri:  "redis+sentinel://host:abc?master=m",
			want: "invalid port",
		},
		{
			name: "out-of-range port",
			uri:  "redis+sentinel://host:99999?master=m",
			want: "out of range",
		},
		{
			name: "port zero",
			uri:  "redis+sentinel://host:0?master=m",
			want: "out of range",
		},
		{
			name: "malformed userinfo with two colons",
			uri:  "redis+sentinel://user:pass:word@host:26379?master=m",
			want: "malformed userinfo",
			hide: "pass:word",
		},
		{
			name: "empty entry between commas",
			uri:  "redis+sentinel://host1:26379,,host2:26379?master=m",
			want: "empty sentinel address",
		},
		{
			name: "unknown query parameter",
			uri:  "redis+sentinel://host:26379?master=m&foo=bar",
			want: "unknown query parameter",
		},
		{
			name: "typo'd query parameter",
			uri:  "redis+sentinel://host:26379?mastery=x",
			want: "unknown query parameter",
		},
		{
			name: "duplicate master query parameter",
			uri:  "redis+sentinel://host:26379?master=a&master=b",
			want: "master specified multiple times",
		},
		{
			name: "invalid DB path",
			uri:  "redis+sentinel://host:26379/abc?master=m",
			want: "invalid redis DB index",
		},
		{
			name: "DB index out of range",
			uri:  "redis+sentinel://host:26379/16?master=m",
			want: "out of range",
		},
		{
			name: "unsupported scheme",
			uri:  "redis://host:6379",
			want: "unsupported scheme",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSentinelURI(tt.uri)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			if tt.hide != "" {
				assert.NotContains(t, err.Error(), tt.hide)
			}
		})
	}
}

func TestParseSentinelURINoCredentialLeak(t *testing.T) {
	// Every failing parse of a URI that embeds credentials must keep the
	// secret out of the error message.
	uris := []string{
		"redis+sentinel://user:s3cret@host:26379",
		"redis+sentinel://user:s3cret@host:26379?master=",
		"redis+sentinel://user:s3cret@host1,,host2:26379?master=m",
		"redis+sentinel://user:s3cret@:26379?master=m",
		"redis+sentinel://user:s3cret@host:99999?master=m",
		"redis+sentinel://user:s3cret@host:26379?mastery=x",
		"redis+sentinel://user:s3cret@host:26379/abc?master=m",
		"redis+sentinel://user:s3cret:extra@host:26379?master=m",
	}
	for _, uri := range uris {
		_, err := parseSentinelURI(uri)
		require.Error(t, err, "uri %q must fail to parse", uri)
		assert.NotContains(t, err.Error(), "s3cret")
		assert.NotContains(t, err.Error(), "redis+sentinel://")
	}
}

func TestParseSentinelURIUnparseableRawURL(t *testing.T) {
	// net/url rejects malformed escapes in the host outright; the error must
	// surface without the URI (url.Error normally embeds it verbatim).
	_, err := parseSentinelURI("redis+sentinel://user:s3cret@ho%st:26379?master=m")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid redis+sentinel URI")
	assert.Contains(t, err.Error(), "invalid URL escape")
	assert.NotContains(t, err.Error(), "s3cret")
}

func TestParseSentinelDB(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    int
		wantErr bool
	}{
		{name: "empty path means DB 0", path: "", want: 0},
		{name: "db 0", path: "/0", want: 0},
		{name: "db 15", path: "/15", want: 15},
		{name: "db 2", path: "/2", want: 2},
		{name: "non-numeric", path: "/abc", wantErr: true},
		{name: "empty index", path: "/", wantErr: true},
		{name: "negative", path: "/-1", wantErr: true},
		{name: "too large", path: "/16", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr {
				_, err := parseSentinelDB(tt.path)
				assert.Error(t, err)
				return
			}
			db, err := parseSentinelDB(tt.path)
			require.NoError(t, err)
			assert.Equal(t, tt.want, db)
		})
	}
}

func TestNewClientSentinelInvalidURINoFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// A redis+sentinel URI that fails sentinel parsing must fail loudly and
	// must not be re-parsed as a standalone redis:// URI.
	_, err := NewClient(ctx, Config{URI: "redis+sentinel://sentinel-1:26379"}, discardLogger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse redis sentinel URI")
	assert.Contains(t, err.Error(), "master")
	assert.NotContains(t, err.Error(), "parse redis URI")
}

func TestNewClientStandaloneURIBadURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := NewClient(ctx, Config{URI: "redis://host:notaport"}, discardLogger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse redis URI")
	assert.NotContains(t, err.Error(), "parse redis sentinel URI")
}

func TestSentinelFailoverOptions(t *testing.T) {
	t.Run("credentials applied to sentinel and data nodes", func(t *testing.T) {
		opts := sentinelFailoverOptions(sentinelConfig{
			Addrs:      []string{"sentinel-1:26379"},
			Username:   "user",
			Password:   "s3cret",
			MasterName: "mymaster",
			DB:         2,
		})
		assert.Equal(t, "mymaster", opts.MasterName)
		assert.Equal(t, []string{"sentinel-1:26379"}, opts.SentinelAddrs)
		assert.Equal(t, "user", opts.Username)
		assert.Equal(t, "s3cret", opts.Password)
		assert.Equal(t, 2, opts.DB)
		assert.Equal(t, "user", opts.SentinelUsername)
		assert.Equal(t, "s3cret", opts.SentinelPassword)
	})

	t.Run("username only sets sentinel username and empty password", func(t *testing.T) {
		opts := sentinelFailoverOptions(sentinelConfig{
			Addrs:      []string{"sentinel-1:26379"},
			Username:   "user",
			MasterName: "m",
		})
		assert.Equal(t, "user", opts.SentinelUsername)
		assert.Empty(t, opts.SentinelPassword)
		assert.Equal(t, "user", opts.Username)
		assert.Empty(t, opts.Password)
	})

	t.Run("password only sets sentinel password", func(t *testing.T) {
		opts := sentinelFailoverOptions(sentinelConfig{
			Addrs:      []string{"sentinel-1:26379"},
			Password:   "pw",
			MasterName: "m",
		})
		assert.Empty(t, opts.SentinelUsername)
		assert.Equal(t, "pw", opts.SentinelPassword)
		assert.Empty(t, opts.Username)
		assert.Equal(t, "pw", opts.Password)
	})

	t.Run("no credentials leaves sentinel auth empty", func(t *testing.T) {
		opts := sentinelFailoverOptions(sentinelConfig{
			Addrs:      []string{"sentinel-1:26379"},
			MasterName: "m",
		})
		assert.Empty(t, opts.SentinelUsername)
		assert.Empty(t, opts.SentinelPassword)
		assert.Empty(t, opts.Username)
		assert.Empty(t, opts.Password)
		assert.Equal(t, "m", opts.MasterName)
	})
}

func TestNewSentinelClientIsUniversalClient(t *testing.T) {
	client := newSentinelClient(sentinelConfig{
		Addrs:      []string{"127.0.0.1:26379"},
		MasterName: "m",
	})
	defer client.Close()

	var _ redis.UniversalClient = client
}

func TestNewSentinelClientPingErrorNoCredentialLeak(t *testing.T) {
	// The real NewFailoverClient path (no seam): dialing unreachable Sentinel
	// nodes must surface an error naming sentinel and must not leak the
	// password into the message.
	client := newSentinelClient(sentinelConfig{
		Addrs:      []string{"127.0.0.1:1"},
		MasterName: "m",
		Password:   "s3cret",
	})
	defer client.Close()

	// go-redis retries each command until the caller's deadline expires, and
	// an expired deadline surfaces as a bare "context deadline exceeded" that
	// masks the underlying sentinel error. Restricting the client to a single
	// attempt lets the real sentinel error surface quickly; the deadline then
	// only bounds the test.
	client.Options().MaxRetries = 0

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := client.Ping(ctx).Err()
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "sentinel")
	assert.NotContains(t, err.Error(), "s3cret")
}
