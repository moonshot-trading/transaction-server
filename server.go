package main

import (
	"fmt"
	"net/http"
	"net"
	"bufio"
	"time"
	"strconv"
	"math/rand"
	"encoding/json"
	"database/sql"

	_ "github.com/lib/pq"
)

type Quote struct {
	Price int
	StockSymbol string
	UserId string
	Timestamp int
	CryptoKey int
}

var (
	quoteServerURL = "localhost"
	quoteServerPort = 44415
)

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "Transaction server connection successful")
}

func quoteHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId string
		StockSymbol string
	}{"", ""}

	err := decoder.Decode(&req)

	if err != nil || len(req.StockSymbol) < 3 {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	commandString := req.StockSymbol + "," + req.UserId
	quote := Quote{}

	//	Check if the user exists, and if they have the necessary balance to request a quote

	//	Make request to the quote service
	conn, err := net.Dial("tcp", "localhost:44415")
	failOnError(err, "Unable to connect to quote service")

	fmt.Fprintf(conn, commandString + "\n")
	quotedPrice, _ := bufio.NewReader(conn).ReadString('\n')

	//	Apply the charge to the users account in the database

	//	Return quote back to requester
	priceString, err := strconv.ParseInt(quotedPrice, 10, 64)
	quote.Price = int(priceString)
	quote.StockSymbol = req.StockSymbol
	quote.UserId = req.UserId
	//	Need to update the test quote server to generate this timestamp and cryptokey
	quote.Timestamp = int(time.Now().Unix())
	quote.CryptoKey = rand.Intn(99999999 - 10000000) + 10000000

	quoteJson, err := json.Marshal(quote)
	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, string(quoteJson))
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
	rand.Seed(time.Now().Unix())
	_ = loadDB()
	port := ":44416"
	fmt.Printf("Listening on port %s\n", port)
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/quote", quoteHandler)
	http.ListenAndServe(port, nil)
}