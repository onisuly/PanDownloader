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
)

const (
	confName   = "pandownloader.cfg"
	preSplit   = uint64(10240)
	preSize    = uint64(32)
	panErrSize = 200
)

var downloadSize = 0
var client *http.Client

var url = flag.String("url", "", "url to download")
var split = flag.Uint64("split", preSplit, "file split count")
var size = flag.Uint64("chunksize", preSize, "chunk size")
var bduss = flag.String("bduss", "", "BDUSS cookie")
var debug = flag.Bool("debug", false, "enable debug mode")

func init() {
	flag.Parse()
	loadConf()

	client = &http.Client{}
}

func main() {
	err := parallelDownload(*url, *split, *size)
	if err != nil {
		panic(err)
	}
}

func parallelDownload(url string, split uint64, chunkSize uint64) error {
	filename, length, err := parseHeader(url)
	if err != nil {
		return err
	}

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Printf("File size: %s\n", humanReadableByteCount(length))
	if length < uint64(chunkSize) {
		chunkSize = length
		split = 1
	}
	lenSub := length / split
	extra := length % split

	fmt.Println("download start")
	var wg sync.WaitGroup
	for i := uint64(0); i < split; i++ {
		wg.Add(1)

		start := lenSub * i
		end := start + lenSub

		if i == split-1 {
			end += extra
		}

		go func(start uint64, end uint64, chunkSize uint64) {
			for err := download(url, file, start, end, chunkSize); err != nil; {
				if *debug {
					log.Println(err)
				}
				err = download(url, file, start, end, chunkSize)
			}
			printProgress()
			wg.Done()
		}(start, end, chunkSize)
	}

	wg.Wait()

	fmt.Println("\ndownload completed")
	return nil
}

func download(url string, file *os.File, start uint64, end uint64, chunkSize uint64) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.AddCookie(&http.Cookie{Name: "BDUSS", Value: *bduss})

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.ContentLength < panErrSize {
		bytes, _ := ioutil.ReadAll(resp.Body)
		var panErr panError
		err := json.Unmarshal(bytes, &panErr)
		if err == nil && panErr.ErrorCode != 0 {
			return errors.New(panErr.ErrorMsg)
		}
	}

	reader := bufio.NewReader(resp.Body)
	position := start
	part := make([]byte, chunkSize)

	for {
		count, err := reader.Read(part)
		file.WriteAt(part[:count], int64(position))
		position += uint64(count)

		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
	}

	return nil
}

func parseHeader(url string) (filename string, length uint64, err error) {
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return "", 0, err
	}
	req.AddCookie(&http.Cookie{Name: "BDUSS", Value: *bduss})

	res, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}

	maps := res.Header
	length, err = strconv.ParseUint(maps["Content-Length"][0], 10, 64)
	if err != nil {
		return "", 0, err
	}

	if length < panErrSize {
		resp, _ := client.Get(url)
		bytes, _ := ioutil.ReadAll(resp.Body)
		var panErr panError
		err := json.Unmarshal(bytes, &panErr)
		if err == nil && panErr.ErrorCode != 0 {
			return "", 0, errors.New(panErr.ErrorMsg)
		}
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

func loadConf() {
	ex, _ := os.Executable()
	confPath := path.Join(filepath.Dir(ex), confName)
	if _, err := os.Stat(confPath); !os.IsNotExist(err) {
		var cfg panConf
		bytes, err := ioutil.ReadFile(confPath)
		if err != nil {
			return
		}
		err = json.Unmarshal(bytes, &cfg)
		if err != nil {
			return
		}

		if *url == "" {
			*url = cfg.URL
		}
		if cfg.Split != 0 && *split == preSplit {
			*split = cfg.Split
		}
		if cfg.Size != 0 && *size == preSize {
			*size = cfg.Size
		}
		if *bduss == "" {
			*bduss = cfg.BDUSS
		}
	}
}

func printProgress() {
	downloadSize++
	fmt.Print("\033[2K")
	fmt.Printf("\r%d / %d blocks downloaded", downloadSize, *split)
}
