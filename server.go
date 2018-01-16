package main

import (
	"fmt"
	"net/http"
	"encoding/json"
	"database/sql"

	_ "github.com/lib/pq"
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

func loadDB() *sql.DB {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", "localhost", 5432, "moonshot", "hodl", "moonshot")
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		failGracefully(err, "Failed to open Postgres")
	}
	err = db.Ping()
	if err != nil {
		failGracefully(err, "Failed to Ping Postgres")
	} else {
		fmt.Println("Connectd to DB")
	}
	return db
}

func main() {
	_ = loadDB()
	port := ":44416"
	fmt.Printf("Listening on port %s\n", port)
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/quote", quoteHandler)
	http.ListenAndServe(port, nil)
}