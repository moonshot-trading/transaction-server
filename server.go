package main

import (
	"fmt"
	"net/http"
	"net"
	"time"
	"strings"
	"strconv"
	"math/rand"
	"errors"
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

func getQuote(commandString string, UserId string) (string, error) {
	//	Check if the user exists, and if they have the necessary balance to request a quote
	queryString := "SELECT funds FROM users WHERE user_name = $1"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		return "", err
	}

	var funds int
	err = stmt.QueryRow(UserId).Scan(&funds)

	if err != nil {
		return "", err
	}

	if funds < 5 {
		//	Not enough money to get a quote
		return "", errors.New("Insufficient Funds")
	}

	conn, err := net.Dial("tcp", "localhost:44415")
	defer conn.Close()

	if err != nil {
		fmt.Println("Connection error")
	}

	conn.Write([]byte(commandString + "\n"))
	buff := make([]byte, 1024)
	length, _ := conn.Read(buff)

	//	Apply the charge to the users account in the database
	queryString = "UPDATE users SET funds = $1 WHERE user_name = $2"
	stmt, err = db.Prepare(queryString)

	if err != nil {
		return "", err
	}

	res, err := stmt.Exec(funds - 5, UserId)

	if err != nil {
		return "", err
	}

	numRows, err := res.RowsAffected()

	if err != nil || numRows < 1 {
		return "", errors.New("Couldn't Charge Account")
	}

	quoteString := string(buff[:length])
	return quoteString, nil
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
	quoteString, err := getQuote(commandString, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	quoteStringComponents := strings.Split(quoteString, ",")

	quote := Quote{}
	quote.Price = quoteStringComponents[0]
	quote.StockSymbol = quoteStringComponents[1]
	quote.UserId = quoteStringComponents[2]
	quote.Timestamp, _ = strconv.Atoi(quoteStringComponents[3])
	quote.CryptoKey = quoteStringComponents[4]

	quoteJson, err := json.Marshal(quote)
	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, string(quoteJson))
}

func addHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId string
		Amount int
	}{"", 0}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	queryString := "INSERT INTO users(user_name, funds) VALUES($1, $2) ON CONFLICT (user_name) DO UPDATE SET funds = users.funds + $2"
	stmt, err := db.Prepare(queryString)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	res, err := stmt.Exec(req.UserId, req.Amount)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	numRows, err := res.RowsAffected()

	if numRows < 1 {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func buyHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	req := struct {
		UserId string
		StockSymbol string
		Amount int
	}{"", "", 0}

	err := decoder.Decode(&req)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusBadRequest), w, http.StatusBadRequest)
		return
	}

	//	Get a quote
	commandString := req.UserId + "," + req.StockSymbol
	quoteString, err := getQuote(commandString, req.UserId)

	if err != nil {
		failWithStatusCode(err, http.StatusText(http.StatusInternalServerError), w, http.StatusInternalServerError)
		return
	}

	// Keep track of buy command to be committed later

	//	Adjust account balance

	//	Send response back to client
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
	http.HandleFunc("/add", addHandler)
	http.HandleFunc("/buy", buyHandler)
	http.ListenAndServe(port, nil)
}