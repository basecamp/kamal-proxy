package server

import (
	"sync"
)

func PerformConcurrently(fns ...func()) {
	var wg sync.WaitGroup

	wg.Add(len(fns))

	for _, fn := range fns {
		go func() {
			defer wg.Done()
			fn()
		}()
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
