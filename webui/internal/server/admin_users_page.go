package server

import "github.com/home-server-project/justvoxel-webui/internal/api"

func apiMessage(err error, fallback string) string {
	if message, ok := api.ErrorMessage(err); ok {
		return message
	}
	return fallback
}
