package main

type Buy struct {
	BuyTimestamp	int
	QuoteTimestamp	int
	QuoteCryptoKey	string
	StockSymbol		string
	StockPrice		float64
	BuyAmount		int
}

type Sell struct {
	SellTimestamp	int
	QuoteTimestamp	int
	QuoteCryptoKey	string
	StockSymbol		string
	StockPrice		float64
	SellAmount		int
	StockSellAmount	int
}