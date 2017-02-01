package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/influxdata/influxdb/client/v2"
)

type InfluxDBConfig struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
}

type WebSocketConfig struct {
	Host     string
	Port     int
	User     string
	Password string
}

type Configuration struct {
	InfluxDB        InfluxDBConfig
	WebSocketServer WebSocketConfig
	Id              string
}

type CalaosJsonMsg struct {
	Msg   string `json:"msg"`
	MsgID string `json:"msg_id"`
}

type CalaosJsonMsgLogin struct {
	Msg  string `json:"msg"`
	Data struct {
		Success string `json:"success"`
	} `json:"data"`
	MsgID string `json:"msg_id"`
}

type CalaosJsonMsgEvent struct {
	Msg  string `json:"msg"`
	Data struct {
		EventRaw string `json:"event_raw"`
		Data     struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"data"`
		TypeStr string `json:"type_str"`
		Type    string `json:"type"`
	} `json:"data"`
}

type CalaosJsonMsgHome struct {
	Msg  string `json:"msg"`
	Data struct {
		Home []struct {
			Type  string `json:"type"`
			Hits  string `json:"hits"`
			Name  string `json:"name"`
			Items []struct {
				Visible string `json:"visible"`
				VarType string `json:"var_type"`
				ID      string `json:"id"`
				IoType  string `json:"io_type"`
				Name    string `json:"name"`
				Type    string `json:"type"`
				GuiType string `json:"gui_type"`
				State   string `json:"state"`
				Rw      string `json:"rw,omitempty"`
			} `json:"items"`
		} `json:"home"`
		Cameras []interface{} `json:"cameras"`
		Audio   []interface{} `json:"audio"`
	} `json:"data"`
	MsgID string `json:"msg_id"`
}

var loggedin bool
var home CalaosJsonMsgHome
var configFilename string
var config Configuration

func getNameFromId(id string) string {
	for i := range home.Data.Home {
		for j := range home.Data.Home[i].Items {
			if home.Data.Home[i].Items[j].ID == id {
				log.Println("ID " + id + " == Name " + home.Data.Home[i].Items[j].Name)
				return home.Data.Home[i].Items[j].Name
			}
		}

	}
	return ""

}

func main() {

	flag.StringVar(&configFilename, "config", "./config.json", "Get the config to use. default value is ./config.json")
	flag.Parse()
	// Set up channel on which to send signal notifications.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, os.Kill)

	log.Print("Opening Configuration filename : " + configFilename)
	file, err := os.Open(configFilename)
	if err != nil {
		log.Println("error:", err)
		os.Exit(1)
	}
	decoder := json.NewDecoder(file)

	err = decoder.Decode(&config)
	if err != nil {
		log.Println("error:", err)
	}
	log.Println("Configuration : ")
	log.Println(config.WebSocketServer)
	log.Println(config.InfluxDB)
	log.Println(config.Id)

	loggedin = false

	log.Println("Opening :", "ws://"+config.WebSocketServer.Host+":"+strconv.Itoa(config.WebSocketServer.Port)+"/api")

	websocket_client, _, err := websocket.DefaultDialer.Dial("ws://"+config.WebSocketServer.Host+":"+strconv.Itoa(config.WebSocketServer.Port)+"/api", nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer websocket_client.Close()

	done := make(chan struct{})

	msg := "{ \"msg\": \"login\", \"msg_id\": \"1\", \"data\": { \"cn_user\": \"" + config.WebSocketServer.User + "\", \"cn_pass\": \"" + config.WebSocketServer.Password + "\" } }"
	if err = websocket_client.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		log.Println("Write message error")
		return
	}

	go func() {

		// Create a new HTTPClient
		c, err := client.NewHTTPClient(client.HTTPConfig{
			Addr: "http://localhost:8086",
		})
		if err != nil {
			log.Fatal(err)
		}

		// Create a new point batch
		bp, err := client.NewBatchPoints(client.BatchPointsConfig{
			Database:  "calaos",
			Precision: "s",
		})
		if err != nil {
			log.Fatal(err)
		}

		defer websocket_client.Close()
		defer close(done)
		for {
			_, message, err := websocket_client.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			log.Printf("recv: %s", message)
			var msg CalaosJsonMsg
			err = json.Unmarshal([]byte(message), &msg)
			if err != nil {
				log.Println("error:", err)
			}

			if msg.Msg == "login" {
				var loginMsg CalaosJsonMsgLogin
				err = json.Unmarshal([]byte(message), &loginMsg)
				if err != nil {
					log.Println("error:", err)
				}
				if loginMsg.Data.Success == "true" {
					loggedin = true
					log.Printf("Loggedin")
					getHomeMsg := "{ \"msg\": \"get_home\", \"msg_id\": \"2\" }"
					if err = websocket_client.WriteMessage(websocket.TextMessage, []byte(getHomeMsg)); err != nil {
						log.Println("Write message error")
						return
					}

				} else {
					loggedin = false
				}
			}

			if loggedin {
				if msg.Msg == "event" {

					var eventMsg CalaosJsonMsgEvent
					err = json.Unmarshal([]byte(message), &eventMsg)
					if err != nil {
						log.Println("error:", err)
					}
					name := getNameFromId(eventMsg.Data.Data.ID)
					log.Printf("%s", name)
					//data := "{\"" + name + "\":" + eventMsg.Data.Data.State + "}"
					//fmt.Printf("%s\n", data)

					// Create a point and add to batch
					//					tags := map[string]string{"cpu": "cpu-total"}
					fields := map[string]interface{}{
						name: eventMsg.Data.Data.State,
					}

					log.Println("Config.Id ", config.Id)
					pt, err := client.NewPoint(config.Id, nil, fields, time.Now())
					if err != nil {
						log.Fatal(err)
					}
					bp.AddPoint(pt)

					// Write the batch
					if err := c.Write(bp); err != nil {
						log.Fatal(err)
					}
				}
				if msg.Msg == "get_home" {
					err = json.Unmarshal([]byte(message), &home)
					if err != nil {
						log.Println("error:", err)
					}
					_ = getNameFromId("id")
				}
			}
		}
	}()

	for {
		select {
		case <-sigc:
			log.Println("interrupt")
			// To cleanly close a connection, a client should send a close
			// frame and wait for the server to close the connection.
			err := websocket_client.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
				return
			}
			websocket_client.Close()
			return
		}
	}

}
