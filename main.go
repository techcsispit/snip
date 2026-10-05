package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	storePath := "links.json"
	if path := os.Getenv("STORE_PATH"); path != "" {
		storePath = path
	}

	srv := NewServer(storePath)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("snip running at http://localhost:%s", port)
		if err := http.ListenAndServe(":"+port, srv); err != nil {
			log.Fatal(err)
		}
	}()

	<-stop
	log.Println("shutting down...")
	srv.store.Close()
}
