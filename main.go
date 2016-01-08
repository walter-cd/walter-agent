package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/walter-cd/walter-server/api"
)

var server string

func main() {
	flags := flag.NewFlagSet("walter-agent", flag.ExitOnError)
	flags.StringVar(&server, "server", "http://localhost:8080/", "URL of walter-server")

	if err := flags.Parse(os.Args[1:]); err != nil {
		panic(err)
	}

	queue := make(chan api.Job)
	done := make(chan bool)
	go pollJob(queue)
	go processJob(queue)
	<-done
}

func pollJob(queue chan api.Job) {
	for {
		res, _ := http.Get(fmt.Sprintf("%s/api/v1/jobs/pop", server))
		fmt.Printf("%s\n", res.Status)

		rb := bufio.NewReader(res.Body)
		var body string
		for {
			s, err := rb.ReadString('\n')
			body = body + s
			if err == io.EOF {
				break
			}
		}

		if res.Status == "200 OK" {
			var job api.Job
			err := json.Unmarshal([]byte(body), &job)
			if err != nil {
				panic(err)
			}
			queue <- job
		}
		time.Sleep(1 * 1000000000)
	}
}

func processJob(queue chan api.Job) {
	maxJobCount := 5
	jobCount := 0
	done := make(chan bool)

	for {
		fmt.Println("Waiting job ...")
		if jobCount < maxJobCount {
			job := <-queue
			jobCount++
			go runWalter(job, done)
		}

		select {
		case <-done:
			jobCount--
		default:
			time.Sleep(1 * 1000000000)
		}
	}
}

func runWalter(job api.Job, done chan bool) {
	fmt.Printf("%#v\n", job)
	time.Sleep(100 * 1000000000)
	done <- true
}
