package main

import (
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"time"
)

func primeFinder[T int](done <-chan int, randStream <-chan int) <-chan int {
	isPrime := func(randomInt int) bool {
		for i := randomInt - 1; i > 1; i-- {
			if randomInt%i == 0 {
				return false
			}
		}
		return true
	}
	primes := make(chan int)
	go func() {
		defer close(primes)
		for {
			select {
			case <-done:
				return
			case randomInt := <-randStream:
				if isPrime(randomInt) {
					primes <- randomInt
				}
			}
		}
	}()
	return primes
}
func gen[T any, K any](done <-chan K, fn func() T) <-chan T {
	stream := make(chan T)
	go func() {
		defer close(stream)
		for {
			select {
			case <-done:
				return
			case stream <- fn():
			}
		}
	}()
	return stream
}

func take[T any](done <-chan int, stream <-chan T, num int) <-chan T {
	result := make(chan T)
	go func() {
		defer close(result)
		for i := 0; i < num; i++ {
			select {
			case <-done:
				return
			case result <- <-stream:
			}
		}
	}()
	return result
}

func fanIn[T any](done <-chan int, channels ...<-chan T) <-chan T {
	fanInStream := make(chan T)
	var wg sync.WaitGroup

	transfers := func(ch <-chan T) {
		defer wg.Done()
		for i := range ch {
			select {
			case <-done:
				return
			case fanInStream <- i:
			}
		}
	}

	for _, ch := range channels {
		wg.Add(1)
		go transfers(ch)
	}

	go func() {
		wg.Wait()
		close(fanInStream)
	}()
	return fanInStream
}

func main() {
	start := time.Now()
	done := make(chan int)
	defer close(done)

	randNumFetcher := func() int { return rand.Intn(5000000) }

	randStream := gen(done, randNumFetcher)

	// primeStream := primeFinder(done, randStream)

	// Naive approach
	// for randomNum := range take(done, primeStream, 10) {
	// 	fmt.Println(randomNum)
	// }

	// fan out fan in pattern

	CPUCount := runtime.NumCPU()
	primeFinderChannels := make([]<-chan int, CPUCount)
	for i := 0; i < CPUCount; i++ {
		primeFinderChannels[i] = primeFinder(done, randStream)
	}
	fanInStream := fanIn(done, primeFinderChannels...)
	for randomNum := range take(done, fanInStream, 10) {
		fmt.Println(randomNum)
	}
	duration := time.Since(start)
	// print in secs
	fmt.Println("Time taken:", duration.Seconds())
	fmt.Println("Number of CPUs:", runtime.NumCPU())
}
