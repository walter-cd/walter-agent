package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/walter-cd/walter-server/api"
)

var server string
var maxWorkers int64
var workers int64 = 0
var interval int64
var workingDir string

func main() {
	flags := flag.NewFlagSet("walter-agent", flag.ExitOnError)
	flags.StringVar(&server, "server", "http://localhost:8080/", "URL of walter-server")
	flags.Int64Var(&maxWorkers, "max-workers", 5, "Maximum number of walter workers")
	flags.Int64Var(&interval, "internal", 1, "Job polling interval by seconds")
	flags.StringVar(&workingDir, "working-dir", "/var/tmp/walter", "Working directory")

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
		time.Sleep(time.Duration(interval) * time.Second)

		if workers >= maxWorkers {
			continue
		}

		res, _ := http.Get(fmt.Sprintf("%s/api/v1/jobs/pop", server))

		if res.Status == "200 OK" {
			rb := bufio.NewReader(res.Body)
			var body string
			for {
				s, err := rb.ReadString('\n')
				body = body + s
				if err == io.EOF {
					break
				}
			}

			var job api.Job
			err := json.Unmarshal([]byte(body), &job)
			if err != nil {
				panic(err)
			}
			queue <- job
		}
	}
}

func processJob(queue chan api.Job) {
	done := make(chan bool)

	for {
		select {
		case job := <-queue:
			go runWalter(job, done, workers)
			workers++
		case <-done:
			workers--
		}
	}
}

func runWalter(job api.Job, done chan bool, num int64) {
	workerDir := workingDir + "/" + strconv.FormatInt(num, 10)
	if err := os.MkdirAll(workerDir, 0755); err != nil {
		panic(err)
	}

	repoDir := workerDir + "/" + job.Project
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		panic(err)
	}

	exec.Command("git", "clone", job.CloneUrl, repoDir).CombinedOutput()
	os.Chdir(repoDir)
	exec.Command("git", "checkout", job.Revision).CombinedOutput()

	done <- true
}
