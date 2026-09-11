package cli

import (
	"log"

	"conduit/internal/api"
	"conduit/internal/config"
)

// runServer starts the API server for the "server" command.
func runServer(logger *log.Logger) error {
	cfg := config.Load(logger)
	return api.Run(cfg, logger)
}
