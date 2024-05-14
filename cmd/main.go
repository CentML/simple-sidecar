package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/centml/simple-sidecar/pkg/webhook"
	"github.com/spf13/viper"
)

var (
	infoLogger    *log.Logger
	warnLogger *log.Logger
	errorLogger   *log.Logger
)

func init() {
	// init loggers
	infoLogger = log.New(os.Stderr, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	warnLogger = log.New(os.Stderr, "WARN: ", log.Ldate|log.Ltime|log.Lshortfile)
	errorLogger = log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)

	viper.AutomaticEnv()
	viper.SetDefault("PORT", 8443)
	viper.SetDefault("CONFIG_FILE", "/etc/webhook/config/sidecarconfig.yaml")
	viper.SetDefault("CERT_FILE", "/etc/webhook/certs/tls.crt")
	viper.SetDefault("KEY_FILE", "/etc/webhook/certs/tls.key")
}

func main() {
	sidecarConfigs, err := webhook.LoadConfig(viper.GetString("CONFIG_FILE"))
	if err != nil {
		errorLogger.Fatalf("Failed to load configuration: %v", err)
	}

	cfg := &webhook.WebhookServerConfig{
		Port:           viper.GetInt("PORT"),
		CertPEM:        viper.GetString("CERT_FILE"),
		KeyPEM:         viper.GetString("KEY_FILE"),
		SidecarConfigs: sidecarConfigs,
		InfoLogger:     infoLogger,
		WarnLogger:     warnLogger,
		ErrorLogger:    errorLogger,
	}
	whsvr := webhook.NewWebhookServer(cfg)

	// start webhook server in new rountine
	go func() {
		if err := whsvr.Start(); err != nil {
			errorLogger.Fatalf("Failed to start webhook server: %v", err)
		}
	}()

	// listening OS shutdown singal
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	infoLogger.Printf("Got OS shutdown signal, shutting down webhook server gracefully...")
	whsvr.Stop()
}
