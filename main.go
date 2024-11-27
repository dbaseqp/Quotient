package main

import (
	"flag"
	"log"
	"log/slog"
	"os"

	"quotient/engine"
	"quotient/engine/config"
	"quotient/engine/db"
	"quotient/www"
)

var logLvels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

var opts struct {
	logger struct {
		level string
	}
}

func main() {
	// parse command line options
	flag.StringVar(&opts.logger.level, "log-level", "debug", "Set the log level")
	flag.Parse()

	logLevel, ok := logLvels[opts.logger.level]
	if !ok {
		log.Fatalf("Invalid log level: %s", opts.logger.level)
	}
	// use config to setup engine
	var handler slog.Handler
	handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// read config file
	conf := config.ConfigSettings{}
	if err := conf.SetConfig("./config/event.conf"); err != nil {
		log.Fatalf("Error reading config file: %v", err)
		return
	}

	// create engine and connect to db
	se := engine.NewEngine(&conf)
	db.Connect(conf.RequiredSettings.DBConnectURL)

	if err := db.AddTeams(&conf); err != nil {
		log.Fatalln("Failed to add teams to DB:", err)
	}

	// load boxes

	if err := db.LoadBoxes(&conf); err != nil {
		log.Fatalln("Failed to load boxes to DB:", err)
	}

	// start engine, restart if it stops
	go func() {
		for {
			se.Start()
		}
	}()

	// start web server
	router := www.Router{Config: &conf, Engine: se}
	router.Start()
}
