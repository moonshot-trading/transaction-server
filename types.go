package main

type Buy struct {
	BuyTimestamp   int64
	QuoteTimestamp int64
	QuoteCryptoKey string
	StockSymbol    string
	StockPrice     int
	BuyAmount      int
}

type Sell struct {
	SellTimestamp   int64
	QuoteTimestamp  int64
	QuoteCryptoKey  string
	StockSymbol     string
	StockPrice      int
	SellAmount      int
	StockSellAmount int
}

type BuyTrigger struct {
	SetBuyTimestamp int64
	StockSymbol     string
	BuyAmount       int
	BuyPrice        int
}

type SellTrigger struct {
	SetSellTimestamp int64
	StockSymbol      string
	SellAmount       int
	SellPrice        int
	StockSellAmount  int
}

type transactionConfig struct {
	quoteServer string
	auditServer string
	db          string
}

//	Auditing types
type AccountTransaction struct {
	Server         string `json:"server"`
	Action         string `json:"action"`
	Username       string `json:"username"`
	Funds          int    `json:"funds"`
	TransactionNum int    `json:"transactionNum"`
}

type SystemEvent struct {
	Server         string `json:"server"`
	Command        string `json:"command"`
	StockSymbol    string `json:"stockSymbol"`
	Username       string `json:"username"`
	Filename       string `json:"filename"`
	Funds          int    `json:"funds"`
	TransactionNum int    `json:"transactionNum"`
}

type ErrorEvent struct {
	Server         string `json:"server"`
	Command        string `json:"command"`
	StockSymbol    string `json:"stockSymbol"`
	Filename       string `json:"filename"`
	Funds          int    `json:"funds"`
	Username       string `json:"username"`
	ErrorMessage   string `json:"errorMessage"`
	TransactionNum int    `json:"transactionNum"`
}

type DebugEvent struct {
	Server         string `json:"server"`
	Command        string `json:"command"`
	StockSymbol    string `json:"stockSymbol"`
	Filename       string `json:"filename"`
	Funds          int    `json:"funds"`
	Username       string `json:"username"`
	DebugMessage   string `json:"debugMessage"`
	TransactionNum int    `json:"transactionNum"`
	Path           string `json:"path"`
}

type QuoteServer struct {
	Server          string `json:"server"`
	Price           int    `json:"price"`
	StockSymbol     string `json:"stockSymbol"`
	Username        string `json:"username"`
	QuoteServerTime int64  `json:"quoteServerTime"`
	Cryptokey       string `json:"cryptokey"`
	TransactionNum  int    `json:"transactionNum"`
}

type UserCommand struct {
	Server         string `json:"server"`
	Command        string `json:"command"`
	Username       string `json:"username"`
	StockSymbol    string `json:"stockSymbol"`
	Filename       string `json:"filename"`
	Funds          int    `json:"funds"`
	TransactionNum int    `json:"transactionNum"`
}
