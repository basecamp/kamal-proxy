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
