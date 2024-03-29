package cmd

import (
	"net/rpc"
	"os"
	"strconv"
)

const (
	ENV_PREFIX = "PARACHUTE_"
)

func withRPCClient(socketPath string, fn func(client *rpc.Client) error) error {
	client, err := rpc.Dial("unix", socketPath)
	if err != nil {
		return err
	}
	defer client.Close()
	return fn(client)
}

func findEnv(key string) (string, bool) {
	value, ok := os.LookupEnv(ENV_PREFIX + key)
	if ok {
		return value, true
	}

	value, ok = os.LookupEnv(key)
	if ok {
		return value, true
	}

	return "", false
}

func getEnvInt(key string, defaultValue int) int {
	value, ok := findEnv(key)
	if !ok {
		return defaultValue
	}

	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}

	return intValue
}

func getEnvBool(key string, defaultValue bool) bool {
	value, ok := findEnv(key)
	if !ok {
		return defaultValue
	}

	boolValue, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}

	return boolValue
}
