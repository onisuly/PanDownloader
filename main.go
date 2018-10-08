package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type param struct {
	url   string
	size  uint64
	block uint64
	chunk uint64
	name  string
	bduss string
	dir   string
	debug bool
}

type task struct {
	url            string
	file           *os.File
	start          uint64
	end            uint64
	chunkSize      uint64
	downloadedSize *uint64
	bduss          string
}

const cfgFileName = "pandownloader.json"

func main() {
	params := parseParams()
	err := parallelDownload(params)
	if err != nil {
		log.Fatalln(err)
	}
}

func parallelDownload(p param) error {
	filename, length, err := parseHeader(p.url, p.bduss)
	if err != nil {
		return err
	}
	if p.name != "" {
		filename = p.name
	}

	file, err := os.Create(path.Join(p.dir, filename))
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Printf("file size: %s\n", formatBytes(length))
	if length < p.block*p.size {
		p.block = map[bool]uint64{true: length / p.size, false: 1}[length/p.size > 0]
	}

	fmt.Printf("download start")
	startTime := time.Now()
	var downloadedSize uint64 = 0
	tasks := createTasks(p.url, file, length, p.block, p.chunk, p.bduss, &downloadedSize)

	var wg sync.WaitGroup
	for i := uint64(0); i < p.size; i++ {
		wg.Add(1)

		go func(tasks chan task) {
			for t := range tasks {
				for err := download(t); err != nil; {
					if p.debug {
						log.Println(err)
					}
					err = download(t)
				}
			}
			wg.Done()
		}(*tasks)
	}

	wg.Add(1)
	printProgress(length, &downloadedSize, func() { wg.Done() })

	wg.Wait()

	fmt.Printf("\ndownload completed, time elapsed: %s, average speed: %s/s\n", time.Since(startTime),
		formatBytes(length/uint64(time.Since(startTime).Seconds())))
	return nil
}

func createTasks(url string, file *os.File, length uint64, block uint64, chunkSize uint64, bduss string,
	downloadedSize *uint64) *chan task {
	tasks := make(chan task)
	split := length / block

	go func() {
		for i := uint64(0); i < split; i++ {
			tasks <- task{
				url:            url,
				file:           file,
				start:          i * block,
				end:            (i + 1) * block,
				chunkSize:      chunkSize,
				bduss:          bduss,
				downloadedSize: downloadedSize,
			}
		}
		if length%block != 0 {
			tasks <- task{
				url:            url,
				file:           file,
				start:          split * block,
				end:            length,
				chunkSize:      chunkSize,
				bduss:          bduss,
				downloadedSize: downloadedSize,
			}
		}
		close(tasks)
	}()

	return &tasks
}

func download(t task) error {
	req, err := http.NewRequest("GET", t.url, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", t.start, t.end))
	req.AddCookie(&http.Cookie{Name: "BDUSS", Value: t.bduss})

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 206 {
		bytes, _ := ioutil.ReadAll(resp.Body)
		return errors.New(string(bytes))
	}

	reader := bufio.NewReader(resp.Body)
	position := t.start
	part := make([]byte, t.chunkSize)

	for {
		count, err := reader.Read(part)
		t.file.WriteAt(part[:count], int64(position))
		position += uint64(count)

		atomic.AddUint64(t.downloadedSize, uint64(count))
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
	}

	return nil
}

func parseHeader(url string, bduss string) (filename string, length uint64, err error) {
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return "", 0, err
	}
	req.AddCookie(&http.Cookie{Name: "BDUSS", Value: bduss})

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}

	if res.StatusCode != 200 {
		resp, _ := client.Get(url)
		defer resp.Body.Close()
		bytes, _ := ioutil.ReadAll(resp.Body)
		return "", 0, errors.New(string(bytes))
	}

	maps := res.Header
	length, err = strconv.ParseUint(maps["Content-Length"][0], 10, 64)
	if err != nil {
		return "", 0, err
	}

	if maps["Content-Disposition"] != nil {
		if _, params, err := mime.ParseMediaType(maps["Content-Disposition"][0]); err == nil {
			filename = params["filename"]
		}
	}
	if filename == "" {
		filename = strings.Split(path.Base(url), "?")[0]
	}

	return filename, length, nil
}

func parseParams() param {
	var url = flag.String("url", "", "url to download, required")
	var size = flag.Uint64("size", 32, "concurrent downloads size")
	var block = flag.Uint64("block", 20971520, "max block size")
	var chunk = flag.Uint64("chunk", 1048576, "max chunk size")
	var name = flag.String("name", "", "download file name")
	var bduss = flag.String("bduss", "", "BDUSS cookie")
	var dir = flag.String("dir", "", "download dir")
	var debug = flag.Bool("debug", false, "enable debug mode")
	flag.Parse()

	if len(os.Args) == 2 {
		*url = os.Args[1]
	}
	if *url == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}

	var p = param{
		url:   *url,
		size:  *size,
		block: *block,
		chunk: *chunk,
		name:  *name,
		bduss: *bduss,
		dir:   *dir,
		debug: *debug,
	}

	flagSet := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { flagSet[f.Name] = true })

	return updateParamsWithCfgFile(p, flagSet)
}

func updateParamsWithCfgFile(p param, flagSet map[string]bool) (result param) {
	result = p

	ex, _ := os.Executable()
	confPath := path.Join(filepath.Dir(ex), cfgFileName)
	if _, err := os.Stat(confPath); !os.IsNotExist(err) {
		bytes, err := ioutil.ReadFile(confPath)
		if err != nil {
			return
		}
		var fileCfg cfgFile
		err = json.Unmarshal(bytes, &fileCfg)
		if err != nil {
			return
		}

		if fileCfg.Size != 0 && !flagSet["size"] {
			result.size = fileCfg.Size
		}
		if fileCfg.Block != 0 && !flagSet["block"] {
			result.block = fileCfg.Block
		}
		if fileCfg.Chunk != 0 && !flagSet["chunk"] {
			result.chunk = fileCfg.Chunk
		}
		if !flagSet["bduss"] {
			result.bduss = fileCfg.BDUSS
		}
		if !flagSet["dir"] {
			result.dir = fileCfg.Dir
		}
	}

	return
}

func printProgress(length uint64, downloadedSize *uint64, done func()) {
	var preDownloadedSize uint64 = 0
	for range time.Tick(time.Second) {
		currentDownloadedSize := *downloadedSize
		oneSecondSize := currentDownloadedSize - preDownloadedSize
		preDownloadedSize = currentDownloadedSize
		fmt.Print("\033[2K")
		fmt.Printf("\r%s / %s downloaded, speed: %s/s", formatBytes(currentDownloadedSize), formatBytes(length),
			formatBytes(oneSecondSize))
		if currentDownloadedSize >= length {
			done()
			return
		}
	}
}
