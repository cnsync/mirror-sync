package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/urfave/cli/v2"
)

type list struct {
	Repository string   `json:"Repository"`
	Tags       []string `json:"Tags"`
}

var FinalTags []string

func main() {
	app := &cli.App{
		Name:  "mirror-sync",
		Usage: "Synchronize Docker images",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "mirror",
				Value: "https://raw.githubusercontent.com/cnsync/mirror-sync/main/mirrors-docker.txt",
				Usage: "name storage address of the mirror to be synchronized",
			},
			&cli.IntFlag{
				Name:  "concurrency",
				Value: 5,
				Usage: "number of concurrent requests",
			},
			&cli.StringFlag{
				Name:  "hub",
				Value: "docker.io/cnxyz",
				Usage: "destination hub for synchronization",
			},
		},
		Action: func(c *cli.Context) error {
			body := httpclient(c.String("mirror"))
			mirrorCtx := strings.Split(body, "\n")
			syncImages(mirrorCtx, c.String("hub"), c.Int("concurrency"))
			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func syncImages(mirrorCtx []string, hub string, maxConcurrency int) {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxConcurrency)

	for _, cmd := range mirrorCtx {
		// 获取源镜像的仓库和标签列表
		srcRepo, srcTags := listTags(cmd)
		if srcRepo == "" {
			log.Println("Empty tags for command:", cmd)
		}

		// 获取源镜像和目标镜像的名称
		srcRe, destRe := getImageNames(srcRepo, hub)
		_, destTag := listTags(destRe)

		if destTag != nil {
			// 去除重复的标签，并获取有效的标签列表
			FinalTags = removeDuplicates(srcTags, destTag)
			for _, tag := range getValidTags(FinalTags) {
				wg.Add(1)
				semaphore <- struct{}{} // 申请一个信号量，限制并发数
				go func(src, dest, t string) {
					defer func() {
						<-semaphore // 释放信号量
						wg.Done()
					}()
					copyImage(src, dest, t)
				}(srcRe, destRe, tag)
			}
		} else {
			for _, tag := range getValidTags(srcTags) {
				wg.Add(1)
				semaphore <- struct{}{} // 申请一个信号量，限制并发数
				go func(src, dest, t string) {
					defer func() {
						<-semaphore // 释放信号量
						wg.Done()
					}()
					copyImage(src, dest, t)
				}(srcRe, destRe, tag)
			}
		}
	}

	wg.Wait()
	close(semaphore)
}

func getValidTags(tags []string) []string {
	var validTags []string
	for _, tag := range tags {
		if !strings.Contains(tag, ".sig") {
			validTags = append(validTags, tag)
		}
	}

	// 对标签列表进行排序
	sort.Slice(validTags, func(i, j int) bool {
		return validTags[i] < validTags[j]
	})

	return validTags
}

func listTags(image string) (string, []string) {
	var out bytes.Buffer

	if image != "" {
		// 使用skopeo命令获取镜像的标签信息
		cmd := exec.Command("skopeo", "list-tags", "docker://"+image)
		log.Println("Cmd", cmd.Args)
		cmd.Stdout = &out
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			log.Println("exec.Command command execution error:", err)
			return "", nil
		}

		var l list
		err = json.Unmarshal(out.Bytes(), &l)
		if err != nil {
			log.Println("json.Unmarshal conversion error:", err)
		}

		return l.Repository, l.Tags
	}

	return "", nil
}

func getImageNames(srcRepo, hub string) (string, string) {
	beginIndex := strings.Index(srcRepo, "/")
	rightIndex := strings.LastIndex(srcRepo, "/")

	diffB := srcRepo[beginIndex:rightIndex]
	diffR := srcRepo[rightIndex:]

	if strings.EqualFold(diffB, "/library") {
		str := diffR[1:]
		return srcRepo, hub + "/" + str
	}

	if strings.EqualFold(diffB, diffR) {
		b1 := srcRepo[rightIndex+1:]
		b2 := strings.Replace(b1, "/", "-", -1)
		return srcRepo, hub + "/" + b2
	}

	b1 := srcRepo[beginIndex+1:]
	b2 := strings.Replace(b1, "/", "-", -1)

	return srcRepo, hub + "/" + b2
}

func httpclient(url string) string {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	client := &http.Client{Transport: tr}
	resp, err := client.Get(url)
	if err != nil {
		log.Println("Error:", err)
		return ""
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			return
		}
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("Error:", err)
		return ""
	}
	return string(body)
}

func removeDuplicates(left, right []string) []string {
	set := make(map[string]bool)
	for _, r := range right {
		set[r] = true
	}

	var result []string
	for _, l := range left {
		if !set[l] {
			result = append(result, l)
		}
	}
	return result
}

func copyImage(srcRe, destRe, tag string) {
	cmd := exec.Command(
		"skopeo",
		"copy",
		"--insecure-policy",
		"--src-tls-verify=false",
		"--dest-tls-verify=false",
		"-q",
		"docker://"+srcRe+":"+tag,
		"docker://"+destRe+":"+tag)
	log.Println("Cmd", cmd.Args)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("Error executing command %s: %s\n", cmd.Args, err)
	}
}
