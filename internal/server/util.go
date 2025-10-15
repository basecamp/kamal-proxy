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
