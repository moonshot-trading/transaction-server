package main

type Buy struct {
	BuyTimestamp	int64
	QuoteTimestamp	int64
	QuoteCryptoKey	string
	StockSymbol		string
	StockPrice		float64
	BuyAmount		int
}

type Sell struct {
	SellTimestamp	int64
	QuoteTimestamp	int64
	QuoteCryptoKey	string
	StockSymbol		string
	StockPrice		float64
	SellAmount		int
	StockSellAmount	int
}