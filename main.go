package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	port        = 8098
	cacheDir    = ""
	cacheSize   = 128
	originalURL string

	allowedOrigins []string

	errEmptyCacheDirFlag = errors.New("flag `-cache-dir` is empty")
	errEmptyOriginalFlag = errors.New("flag `-original-url` is empty")

	info = log.New(os.Stdout, "[INFO] ", log.LstdFlags)
	warn = log.New(os.Stderr, "[WARN] ", log.LstdFlags)
	erro = log.New(os.Stderr, "[ERRO] ", log.LstdFlags)
)

func parseFlags() error {
	flag.IntVar(&port, "port", port, fmt.Sprintf("Server port (default:%d)", port))
	flag.StringVar(&cacheDir, "cache-dir", cacheDir, "Directory for cache")
	flag.IntVar(&cacheSize, "cache-size", cacheSize, "Number of image to cache")
	flag.StringVar(&originalURL, "original-url", originalURL, "URL of original image")

	cors := ""
	flag.StringVar(&cors, "cors", cors, "List of domains for CORS")
	flag.Parse()

	allowedOrigins = strings.Split(cors, ",")

	if cacheDir == "" {
		return errEmptyCacheDirFlag
	}

	if originalURL == "" {
		return errEmptyOriginalFlag
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		os.Stderr.WriteString(fmt.Sprintf("%+v\n", err))
		os.Exit(1)
	}
}

func run() error {
	if err := parseFlags(); err != nil {
		return err
	}

	if err := prepareCacheDir(); err != nil {
		return err
	}

	client := &http.Client{Timeout: 3 * time.Second}
	cache := newStore(cacheDir)
	originalURL, err := url.Parse(originalURL)
	if err != nil {
		return err
	}

	http.HandleFunc("/", handler(client, cache, originalURL))
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port)}

	info.Printf("listened on :%d", port)
	errCh := make(chan error)
	go func() {
		defer close(errCh)

		if err := srv.ListenAndServe(); err != nil {
			errCh <- err
		}
	}()

	stopCh := make(chan os.Signal)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}

	case <-stopCh:
		info.Println("terminating ...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			return err
		}
	}

	return nil
}

func handler(client *http.Client, cache *store, originalURL *url.URL) func(rw http.ResponseWriter, r *http.Request) {
	return func(rw http.ResponseWriter, r *http.Request) {
		hash := sha1.Sum([]byte(r.URL.Path + r.URL.RawQuery))
		filename := hex.EncodeToString((hash[:]))
		webpPath := filepath.Join(cacheDir,filename + ".webp")

		if r, err := cache.get(webpPath); err == nil {
			defer r.Close()

			if err := writeWebp(rw, r); err != nil {
				warn.Printf("%+v", err)
			} else {
				return
			
			}
		}

		query := r.URL.Query()
		urlQ := query.Get("url")
		if urlQ == "" {
			http.Error(rw, "query `url` is emptry", http.StatusBadRequest)
			return
		}

		width := query.Get("w")
		if width == "" {
			width = "0"
		}
		height := query.Get("h")
		if height == "" {
			height = "0"
		}

		quality := query.Get("q")

		fullURL := (&url.URL{
			Scheme: originalURL.Scheme,
			Host:   originalURL.Host,
			Path:   urlQ,
		}).String()

		res, err := client.Get(fullURL)
		if err != nil {
			erro.Printf("%+v", err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		defer res.Body.Close()

		origPath := filepath.Join(cacheDir, filename + filepath.Ext(urlQ))
		origFile, err := os.OpenFile(origPath, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			erro.Printf("%+v", err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		defer origFile.Close()

		if _, err := io.Copy(origFile, res.Body); err != nil {
			erro.Printf("%+v", err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		origFile.Close()

		err = cache.set(webpPath, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, "cwebp", "-quiet", "-q", quality, "-resize", width, height, origPath, "-o", webpPath).CombinedOutput(); err != nil {
				return fmt.Errorf("failed to execute cwebp (%s): %w", out, err)
			}
			return nil
		})
		if err != nil {
			erro.Printf("%+v", err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}

		webpFile, err := os.Open(webpPath)
		if err != nil {
			erro.Printf("%+v", err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		defer webpFile.Close()


		if  err := writeWebp(rw, webpFile); err != nil {
			erro.Printf("%+v", err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func prepareCacheDir() error {
	if _, err := os.Stat(cacheDir); err != nil {
		if !os.IsNotExist(err) {
			return err
		}

		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return err
		}
	}

	return nil
}

func writeWebp(rw http.ResponseWriter, r io.Reader) error {
	if _, err := io.Copy(rw, r); err != nil {
		return err
	}

	rw.Header().Add("content-type", "image/webp")
	return nil
}
