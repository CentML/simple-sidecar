package main

import (
	"fmt"
	"os"

	"github.com/centml/simple-sidecar/pkg/webhook"
	"sigs.k8s.io/yaml"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Please provide a config file as a command line argument.")
		os.Exit(1)
	}

	configFile := os.Args[1]

	cfg, err := webhook.LoadConfig(configFile)
	if err != nil {
		fmt.Println(err)
		panic(err)
	}

	yamlData, err := yaml.Marshal(&cfg)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(yamlData))
}
