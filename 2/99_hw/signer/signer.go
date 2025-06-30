package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

func SingleHash(in, out chan interface{}) {
	wg := sync.WaitGroup{}
	mu := sync.Mutex{}

	for data := range in {
		wg.Add(1)
		go func(value interface{}) {
			defer wg.Done()

			num, ok := value.(int)
			if !ok {
				return
			}
			dataStr := strconv.Itoa(num)

			crc32DataCh := make(chan string)
			crc32Md5DataCh := make(chan string)

			go func() {
				crc32DataCh <- DataSignerCrc32(dataStr)
			}()
			go func() {
				mu.Lock()
				md5 := DataSignerMd5(dataStr)
				mu.Unlock()
				crc32Md5DataCh <- DataSignerCrc32(md5)
			}()
			crc32Data := <-crc32DataCh
			crc32Md5Data := <-crc32Md5DataCh

			result := crc32Data + "~" + crc32Md5Data

			fmt.Printf("SingleHash: %d → %s\n", num, result)

			out <- result
		}(data)
	}
	wg.Wait()
}

func MultiHash(in, out chan interface{}) {
	wg := sync.WaitGroup{}
	for data := range in {
		wg.Add(1)
		go func(value interface{}) {
			defer wg.Done()
			hash, ok := value.(string)
			if !ok {
				return
			}

			arrayStrings := make([]string, 6)
			innerWg := &sync.WaitGroup{}

			for i := 0; i < 6; i++ {
				innerWg.Add(1)
				go func(i int, hash string) {
					defer innerWg.Done()
					arrayStrings[i] = DataSignerCrc32(strconv.Itoa(i) + hash)
				}(i, hash)
			}
			innerWg.Wait()
			result := strings.Join(arrayStrings, "")
			fmt.Printf("MultiHash: %s → %s\n", hash, result)

			out <- result
		}(data)
	}
	wg.Wait()
}

func CombineResults(in, out chan interface{}) {
	var results []string
	wg := &sync.WaitGroup{}
	mu := &sync.Mutex{}
	for data := range in {
		wg.Add(1)
		go func(value interface{}) {
			defer wg.Done()
			hash, ok := value.(string)
			if !ok {
				return
			}

			mu.Lock()
			results = append(results, hash)
			mu.Unlock()

		}(data)
	}
	wg.Wait()

	sort.Strings(results)
	out <- strings.Join(results, "_")
}

func ExecutePipeline(jobs ...job) {
	in := make(chan interface{}, 100)
	wg := sync.WaitGroup{}
	for _, currentJob := range jobs {
		out := make(chan interface{}, 100)
		wg.Add(1)
		go func(j job, in, out chan interface{}) {
			defer wg.Done()
			defer close(out)
			j(in, out)
		}(currentJob, in, out)
		in = out
	}
	wg.Wait()
}
