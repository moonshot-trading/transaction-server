package main

import (
	"fmt"
	"net/http"
	"encoding/json"
)

var (
	quoteServerURL = "http://localhost:44417"
)

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "Transaction server connection successful")
}

func quoteHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		userId string
		stockSymbol string
	}{"", ""}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
	}

	//	Check for well formed stock symbol

	//	Check if the user exists, and if they have the necessary balance to request a quote

	//	Make request to the quote service

	//	Return quote back to requester
	
	//	Apply the charge to the users account in the database
}

func main() {
	port := ":44416"
	fmt.Printf("Listening on port %s\n", port)
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/quote", quoteHandler)
	http.ListenAndServe(port, nil)
}