// cmd/edge/main.go
package main

import "go.uber.org/zap"

func main() {
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	logger.Info("canary-edge: Counterpoint poller stub — M5 implementation")
	// Edge runs as a Windows Service alongside Counterpoint + SQL Server.
	// No HTTP port. Polls Counterpoint REST API, emits intelligence packets to GCP.
}
