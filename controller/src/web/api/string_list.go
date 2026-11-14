package api

import (
	cfg "IpacPanel/controller/src/config"
)

func cleanStringList(values []string) []string {
	return cfg.CleanStringList(values)
}
