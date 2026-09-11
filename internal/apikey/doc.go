// Package apikey implements persisted API key management for the Conduit API.
//
// Unlike the former single static bearer token read from the API_KEY
// environment variable, API keys are stored in MongoDB (collection
// config.apiKeys) so they can be created, listed, and revoked without
// redeploying.
//
// Only a SHA-256 hash of each key is persisted, never the key itself. The full
// "sk-..." secret is returned exactly once, at creation time, and cannot be
// recovered afterwards. The Package deliberately exposes no CLI or HTTP code;
// the CLI (internal/cli) and HTTP middleware (internal/api) are separate so the
// domain stays dependency-free and independently testable.
package apikey
