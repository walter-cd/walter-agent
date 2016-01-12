package main

import (
	"bufio"
	"container/list"
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
	"github.com/walter-cd/walter/config"
	"github.com/walter-cd/walter/stages"
	"github.com/walter-cd/walter/walter"
)

var server string
var maxWorkers int64
var workers int64 = 0
var interval int64
var workingDir string

func main() {
	flags := flag.NewFlagSet("walter-agent", flag.ExitOnError)
	flags.StringVar(&server, "server", "http://localhost:8080/", "URL of walter-server")
	flags.Int64Var(&maxWorkers, "max_workers", 5, "Maximum number of walter workers")
	flags.Int64Var(&interval, "internal", 1, "Job polling interval by seconds")
	flags.StringVar(&workingDir, "working_dir", "/var/tmp/walter", "Working directory")

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

	out, err := exec.Command("git", "clone", job.CloneUrl, repoDir).CombinedOutput()
	fmt.Println(string(out))
	fmt.Println(err)

	os.Chdir(repoDir)

	out, err = exec.Command("git", "fetch").CombinedOutput()
	fmt.Println(string(out))
	fmt.Println(err)

	out, err = exec.Command("git", "checkout", job.Revision).CombinedOutput()
	fmt.Println(string(out))
	fmt.Println(err)

	opts := &config.Opts{
		PipelineFilePath: "./pipeline.yml",
		Mode:             "local",
	}

	w, _ := walter.New(opts)
	start := time.Now().Unix()
	result := w.Run()
	end := time.Now().Unix()

	postReport(job, result, w, start, end)

	done <- true
}

func postReport(job api.Job, result bool, w *walter.Walter, start int64, end int64) {
	var status string
	if result {
		status = "success"
	} else {
		status = "fail"
	}

	report := &api.Report{
		Project: &api.Project{
			Name: job.Project,
			Repo: job.HtmlUrl,
		},
		Status:     status,
		Branch:     job.Branch,
		CompareUrl: job.CompareUrl,
		TriggeredBy: &api.User{
			Name:      job.TriggeredBy.Name,
			Url:       job.TriggeredBy.Url,
			AvatarUrl: job.TriggeredBy.AvatarUrl,
		},
		Start: start,
		End:   end,
	}

	for _, commit := range job.Commits {
		report.Commits = append(report.Commits, &api.Commit{
			Revision: commit.Revision,
			Author:   commit.Author,
			Message:  commit.Message,
			Url:      commit.Url,
		})
	}

	for s := w.Engine.Resources.Pipeline.Stages.Front(); s != nil; s = s.Next() {
		stage := s.Value.(*stages.CommandStage)

		var status string
		if stage.GetReturnValue() {
			status = "success"
		} else {
			status = "fail"
		}

		report.Stages = append(report.Stages, &api.Stage{
			Name:   stage.StageName,
			Status: status,
			Stages: getChildStages(stage.ChildStages),
			Out:    stage.OutResult,
			Err:    stage.ErrResult,
			Start:  stage.Start,
			End:    stage.End,
		})
	}

	b, _ := json.Marshal(report)
	fmt.Println(string(b))
}

func getChildStages(l list.List) (st []*api.Stage) {
	for s := l.Front(); s != nil; s = s.Next() {

		stage := s.Value.(*stages.CommandStage)

		var status string
		if stage.GetReturnValue() {
			status = "success"
		} else {
			status = "fail"
		}

		st = append(st, &api.Stage{
			Name:   stage.StageName,
			Status: status,
			Stages: getChildStages(stage.ChildStages),
			Out:    stage.OutResult,
			Err:    stage.ErrResult,
			Start:  stage.Start,
			End:    stage.End,
		})
	}

	return
}

func updateStatus() {
}
