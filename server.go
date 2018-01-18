package main

import (
	"fmt"
	"net/http"
	"net"
	"bufio"
	"time"
	"strings"
	"strconv"
	"math/rand"
	"encoding/json"
	"database/sql"

	_ "github.com/lib/pq"
)

//	Globals
var (
	quoteServerURL = "localhost"
	quoteServerPort = 44415
	db = loadDB()
)

type Quote struct {
	Price string
	StockSymbol string
	UserId string
	Timestamp int
	CryptoKey string
}

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
	queryString := "SELECT funds FROM users WHERE user_name = $1"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	var funds int
	err = stmt.QueryRow(req.UserId).Scan(&funds)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	if funds < 5 {
		//	Not enough money to get a quote
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	//	Make request to the quote service
	conn, err := net.Dial("tcp", "localhost:44415")
	failOnError(err, "Unable to connect to quote service")

	fmt.Fprintf(conn, commandString + "\n")
	quoteString, _ := bufio.NewReader(conn).ReadString('\n')

	quoteStringComponents := strings.Split(quoteString, ",")
	stockPrice := quoteStringComponents[0]
	stockSymbol := quoteStringComponents[1]
	userId := quoteStringComponents[2]
	timestamp, _ := strconv.Atoi(quoteStringComponents[3])
	cryptoKey := quoteStringComponents[4]

	//	Apply the charge to the users account in the database
	queryString = "UPDATE users SET funds = $1 WHERE user_name = $2"
	stmt, err = db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	res, err := stmt.Exec(funds - 5, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	numRows, err := res.RowsAffected()

	if err != nil || numRows < 1 {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	//	Return quote back to requester
	quote.Price = stockPrice
	quote.StockSymbol = stockSymbol
	quote.UserId = userId
	quote.Timestamp = timestamp
	quote.CryptoKey = cryptoKey

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
	port := ":44416"
	fmt.Printf("Listening on port %s\n", port)
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/quote", quoteHandler)
	http.ListenAndServe(port, nil)
}