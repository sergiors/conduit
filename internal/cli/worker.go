package cli

import (
	"log"

	"github.com/sergiors/conduit/internal/config"
	"github.com/sergiors/conduit/internal/worker"
)

// runWorker starts the CDC worker for the "worker" command.
func runWorker(logger *log.Logger) error {
	cfg := config.LoadWorker(logger)
	return worker.Run(cfg, logger)
}
