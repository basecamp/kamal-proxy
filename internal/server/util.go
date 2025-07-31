package server

import (
	"sync"
)

func PerformConcurrently(fns ...func()) {
	var wg sync.WaitGroup

	for _, fn := range fns {
		wg.Go(fn)
	}

	wg.Wait()
}

func Map[T, U any](slice []T, fn func(T) U) []U {
	result := make([]U, len(slice))
	for i, v := range slice {
		result[i] = fn(v)
	}
	return result
}
