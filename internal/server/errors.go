package server

func isChunkedEncodingError(err error) bool {
	if err == nil {
		return false
	}

	// The chunked encoding support in the stdlib returns these failures as plain
	// errors using errors.New, so matching them means string matching on the
	// error message, unfortunately.
	switch err.Error() {
	case "invalid byte in chunk length",
		"http chunk length too large",
		"malformed chunked encoding",
		"trailer header without chunked transfer encoding",
		"too many trailers":
		return true
	}

	return false
}
