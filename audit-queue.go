package main

import (
	"encoding/json"

	"github.com/streadway/amqp"
)

var (
	rmqChannel *amqp.Channel
)

func ErrorAuditer(audits <-chan interface{}) {

	//dump ur shit to the channel
	var err error
	rmqChannel, err = rmqConn.Channel()
	failOnError(err, "Failed to open a channel")
	defer rmqChannel.Close()

	q, err := rmqChannel.QueueDeclare(
		"error_queue", // name
		false,         // durable
		false,         // delete when unused
		false,         // exclusive
		false,         // no-wait
		nil,           // arguments
	)
	failOnError(err, "Failed to declare a queue")

	for auditStruct := range audits {

		body, _ := json.Marshal(auditStruct)

		//dump that shit into rabbitmq

		err = rmqChannel.Publish(
			"",     // exchange
			q.Name, // routing key
			false,  // mandatory
			false,  // immediate
			amqp.Publishing{
				ContentType:     "application/json",
				ContentEncoding: "",
				Body:            []byte(body),
			})
		failOnError(err, "Failed to publish error mq")

	}

}

func TransactionAuditer(audits <-chan interface{}) {

	//dump ur shit to the channel
	var err error
	rmqChannel, err = rmqConn.Channel()
	failOnError(err, "Failed to open a channel")
	defer rmqChannel.Close()

	q, err := rmqChannel.QueueDeclare(
		"transaction_queue", // name
		false,               // durable
		false,               // delete when unused
		false,               // exclusive
		false,               // no-wait
		nil,                 // arguments
	)
	failOnError(err, "Failed to declare a queue")

	for auditStruct := range audits {

		body, _ := json.Marshal(auditStruct)

		//dump that shit into rabbitmq

		err = rmqChannel.Publish(
			"",     // exchange
			q.Name, // routing key
			false,  // mandatory
			false,  // immediate
			amqp.Publishing{
				ContentType:     "application/json",
				ContentEncoding: "",
				Body:            []byte(body),
			})
		failOnError(err, "Failed to publish transaction mq")

	}

}

func UserAuditer(audits <-chan interface{}) {

	//dump ur shit to the channel
	var err error
	rmqChannel, err = rmqConn.Channel()
	failOnError(err, "Failed to open a channel")
	defer rmqChannel.Close()

	q, err := rmqChannel.QueueDeclare(
		"user_queue", // name
		false,        // durable
		false,        // delete when unused
		false,        // exclusive
		false,        // no-wait
		nil,          // arguments
	)
	failOnError(err, "Failed to declare a queue")

	for auditStruct := range audits {

		body, _ := json.Marshal(auditStruct)

		//dump that shit into rabbitmq

		err = rmqChannel.Publish(
			"",     // exchange
			q.Name, // routing key
			false,  // mandatory
			false,  // immediate
			amqp.Publishing{
				ContentType:     "application/json",
				ContentEncoding: "",
				Body:            []byte(body),
			})
		failOnError(err, "Failed to publish user mq")

	}
}

func QuoteAuditer(audits <-chan interface{}) {

	var err error
	rmqChannel, err = rmqConn.Channel()
	failOnError(err, "Failed to open a channel")
	defer rmqChannel.Close()

	q, err := rmqChannel.QueueDeclare(
		"quote_queue", // name
		false,         // durable
		false,         // delete when unused
		false,         // exclusive
		false,         // no-wait
		nil,           // arguments
	)
	failOnError(err, "Failed to declare a queue")

	for auditStruct := range audits {

		body, _ := json.Marshal(auditStruct)

		err = rmqChannel.Publish(
			"",     // exchange
			q.Name, // routing key
			false,  // mandatory
			false,  // immediate
			amqp.Publishing{
				ContentType:     "application/json",
				ContentEncoding: "",
				Body:            []byte(body),
			})
		failOnError(err, "Failed to publish user mq")

	}
}
