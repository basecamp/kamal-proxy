package pages

import "embed"

//go:embed *.html
var DefaultErrorPages embed.FS
