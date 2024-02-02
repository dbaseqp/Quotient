package main

import (
	"fmt"
	"log"
)

func errorPrint(a ...interface{}) {
	log.Printf("[ERROR] %s", fmt.Sprintln(a...))
}

func debugPrint(a ...interface{}) {
	if *debug {
		log.Printf("[DEBUG] %s", fmt.Sprintln(a...))
	}
}
