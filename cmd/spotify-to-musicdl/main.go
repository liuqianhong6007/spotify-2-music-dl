package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"spotify-to-musicdl/internal/app"
	"spotify-to-musicdl/internal/config"
)

var version = "0.1.0"

func main() {
	os.Exit(run())
}

func run() int {
	options, err := config.Parse(os.Args[1:], version)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 2
	}
	if options.Version {
		fmt.Println(version)
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	summary, err := app.New(options, os.Stdout, os.Stderr).Run(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "用户中断，已完成的进度保存在状态文件中。")
			return 130
		}
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 2
	}
	if summary.Failed > 0 {
		return 1
	}
	return 0
}
