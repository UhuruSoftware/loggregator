package main_test

import (
	"code.google.com/p/gogoprotobuf/proto"
	"fmt"
	"github.com/cloudfoundry/dropsonde/events"
	"github.com/cloudfoundry/dropsonde/factories"
	"github.com/cloudfoundry/loggregatorlib/cfcomponent/instrumentation"
	"github.com/cloudfoundry/loggregatorlib/logmessage"
	messagetesthelpers "github.com/cloudfoundry/loggregatorlib/logmessage/testhelpers"
	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"net"
	"net/http"
	"time"
)

// TODO: test error logging and metrics from unmarshaller stage
// messageRouter.Metrics.UnmarshalErrorsInParseEnvelopes++
//messageRouter.logger.Errorf("Log message could not be unmarshaled. Dropping it... Error: %v. Data: %v", err, envelopedLog)

func AddWSSink(receivedChan chan []byte, port string, path string) (*websocket.Conn, chan bool, <-chan bool) {
	dontKeepAliveChan := make(chan bool, 1)
	connectionDroppedChannel := make(chan bool, 1)

	var ws *websocket.Conn

	i := 0
	for {
		var err error
		ws, _, err = websocket.DefaultDialer.Dial("ws://localhost:"+port+path, http.Header{})
		if err != nil {
			i++
			if i > 10 {
				fmt.Printf("Unable to connect to Server in 100ms, giving up.\n")
				return nil, nil, nil
			}
			<-time.After(10 * time.Millisecond)
			continue
		} else {
			break
		}

	}

	ws.SetPingHandler(func(message string) error {
		select {
		case <-dontKeepAliveChan:
			// do nothing
		default:
			ws.WriteControl(websocket.PongMessage, []byte(message), time.Time{})

		}
		return nil
	})

	go func() {
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				close(connectionDroppedChannel)
				close(receivedChan)
				return
			}
			receivedChan <- data
		}

	}()
	return ws, dontKeepAliveChan, connectionDroppedChannel
}

func UnmarshallerMaker(secret string) func([]byte) (*logmessage.Message, error) {
	return func(data []byte) (*logmessage.Message, error) {
		return logmessage.ParseEnvelope(data, secret)
	}
}

func MarshalledLogEnvelope(unmarshalledMessage *logmessage.LogMessage, secret string) []byte {
	envelope := &logmessage.LogEnvelope{
		LogMessage: unmarshalledMessage,
		RoutingKey: proto.String(*unmarshalledMessage.AppId),
	}
	envelope.SignEnvelope(secret)

	return marshalProtoBuf(envelope)
}

func marshalProtoBuf(pb proto.Message) []byte {
	marshalledProtoBuf, err := proto.Marshal(pb)
	if err != nil {
		Fail(err.Error())
	}

	return marshalledProtoBuf
}

func parseProtoBufMessageString(actual []byte) string {
	receivedMessage := &logmessage.LogMessage{}
	err := proto.Unmarshal(actual, receivedMessage)
	if err != nil {
		Fail(err.Error())
	}
	return string(receivedMessage.GetMessage())
}

var _ = Describe("Loggregator Server", func() {

	var receivedChan chan []byte
	var dontKeepAliveChan chan bool
	var connectionDroppedChannel <-chan bool
	var ws *websocket.Conn

	BeforeEach(func() {
		receivedChan = make(chan []byte)
		ws, dontKeepAliveChan, connectionDroppedChannel = AddWSSink(receivedChan, "8083", "/tail/?app=myApp")
		if ws == nil || dontKeepAliveChan == nil || connectionDroppedChannel == nil {

		}

	})

	AfterEach(func(done Done) {
		if dontKeepAliveChan != nil {
			close(dontKeepAliveChan)
			ws.Close()
			Eventually(receivedChan).Should(BeClosed())
			close(done)
		}
	})

	It("works from udp socket to websocket client", func() {

		connection, err := net.Dial("udp", "127.0.0.1:3456")
		Expect(err).To(BeNil())

		expectedMessageString := "Some Data"
		unmarshalledLogMessage := messagetesthelpers.NewLogMessage(expectedMessageString, "myApp")

		expectedMessage := MarshalledLogEnvelope(unmarshalledLogMessage, "secret")

		_, err = connection.Write(expectedMessage)
		Expect(err).To(BeNil())

		receivedMessageString := parseProtoBufMessageString(<-receivedChan)
		Expect(expectedMessageString).To(Equal(receivedMessageString))
	})

	It("drops invalid log envelopes", func() {
		time.Sleep(50 * time.Millisecond)

		connection, err := net.Dial("udp", "127.0.0.1:3456")
		Expect(err).To(BeNil())

		expectedMessageString := "Some Data"
		unmarshalledLogMessage := messagetesthelpers.NewLogMessage(expectedMessageString, "myApp")

		expectedMessage := MarshalledLogEnvelope(unmarshalledLogMessage, "invalid")

		_, err = connection.Write(expectedMessage)
		Expect(err).To(BeNil())
		Expect(receivedChan).To(BeEmpty())
	})

	Context("metric emission", func() {
		var getContext = func(name string) *instrumentation.Context {
			for _, emitter := range loggregatorInstance.Emitters() {
				context := emitter.Emit()
				if context.Name == name {
					return &context
				}
			}
			return nil
		}

		var getMetricValue = func(contextName, metricName string) interface{} {
			context := getContext(contextName)
			if context == nil {
				return nil
			}
			for _, metric := range context.Metrics {
				if metric.Name == metricName {
					return metric.Value
				}
			}
			return nil
		}

		It("emits metrics for the dropsonde message listener", func() {
			countBefore := getMetricValue("dropsondeListener", "receivedMessageCount").(uint64)

			connection, _ := net.Dial("udp", "127.0.0.1:3457")
			connection.Write([]byte{1, 2, 3})

			Eventually(func() interface{} {
				return getMetricValue("dropsondeListener", "receivedMessageCount")
			}).Should(Equal(countBefore + 1))
		})

		It("emits metrics for the dropsonde unmarshaller", func() {
			countBefore := getMetricValue("dropsondeUnmarshaller", "heartbeatReceived").(uint64)

			connection, _ := net.Dial("udp", "127.0.0.1:3457")

			envelope := &events.Envelope{
				Origin:    proto.String("fake-origin-3"),
				EventType: events.Envelope_Heartbeat.Enum(),
				Heartbeat: factories.NewHeartbeat(1, 2, 3),
			}
			message, _ := proto.Marshal(envelope)
			fakeSignature := make([]byte, 32)
			messageWithFakeSignature := append(fakeSignature, message...)
			connection.Write(messageWithFakeSignature)

			Eventually(func() interface{} {
				return getMetricValue("dropsondeUnmarshaller", "heartbeatReceived")
			}).Should(Equal(countBefore + 1))
		})
	})
})
