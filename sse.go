package main

import (

	// "io"
	"encoding/json"
	"io"
	"log"
	"net/http"

	// "os"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

type ClientChan chan gin.H

type Event struct {
	// Events are pushed to this channel by the main events-gathering routine
	Message chan gin.H

	// New client connections
	NewClients chan ClientChan

	// Closed client connections
	ClosedClients chan ClientChan

	// Total client connections
	TotalClients map[ClientChan]bool
}

func NewSSEServer() (event *Event) {
	event = &Event{
		Message:       make(chan gin.H),
		NewClients:    make(chan ClientChan),
		ClosedClients: make(chan ClientChan),
		TotalClients:  make(map[ClientChan]bool),
	}

	go event.listen()

	return
}

func (stream *Event) listen() {
	for {
		select {
		// Add new available client
		case client := <-stream.NewClients:
			stream.TotalClients[client] = true
		// Remove closed client
		case client := <-stream.ClosedClients:
			delete(stream.TotalClients, client)
			close(client)
		// Broadcast message to client
		case eventMsg := <-stream.Message:
			for clientMessageChan := range stream.TotalClients {
				clientMessageChan <- eventMsg
			}
		}
	}
}

func (stream *Event) ServeHTTP() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Initialize client channel
		clientChan := make(ClientChan)

		// Send new connection to event server
		stream.NewClients <- clientChan

		defer func() {
			// Send closed connection to event server
			stream.ClosedClients <- clientChan
		}()

		c.Set("clientChan", clientChan)

		c.Next()
	}
}

func SendSSE(data gin.H) {
	if stream != nil {
		stream.Message <- data
	}
}

func sse(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Transfer-Encoding", "chunked")

	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Continuously send messages to the client
	v, ok := c.Get("clientChan")
	if !ok {
		return
	}
	clientChan, ok := v.(ClientChan)
	if !ok {
		return
	}
	c.Stream(func(w io.Writer) bool {
		// Stream message to client from message channel
		if data, ok := <-clientChan; ok {
			jsonString, err := json.Marshal(data)
			if err != nil {
				log.Printf("%s: %+v", errors.Wrap(err, "SSE json error").Error(), data)
				return false
			}
			// send the message through the available channel
			if !data["admin"].(bool) || claims.Admin {
				c.SSEvent("message", string(jsonString))
				return true
			}
		}
		return false
	})
}
