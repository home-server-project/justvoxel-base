package server

import (
	"context"
	"embed"
)

//go:embed templates/*.html static/*
var assets embed.FS

var _ = context.Background
