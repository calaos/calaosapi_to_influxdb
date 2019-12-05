package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/influxdata/influxdb1-client/v2"
)

type InfluxDBConfig struct {
	Host     string
	Port     int
	Database string
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

type IOBase struct {
	Visible string `json:"visible"`
	VarType string `json:"var_type"`
	ID      string `json:"id"`
	IoType  string `json:"io_type"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	GuiType string `json:"gui_type"`
	State   string `json:"state"`
	Rw      string `json:"rw,omitempty"`
}

type CalaosJsonMsgHome struct {
	Msg  string `json:"msg"`
	Data struct {
		Rooms []struct {
			Type  string   `json:"type"`
			Hits  string   `json:"hits"`
			Name  string   `json:"name"`
			Items []IOBase `json:"items"`
		} `json:"home"`
		Cameras []interface{} `json:"cameras"`
		Audio   []interface{} `json:"audio"`
	} `json:"data"`
	MsgID string `json:"msg_id"`
}

var (
	loggedin       bool
	home           CalaosJsonMsgHome
	configFilename string
	config         Configuration
	ioCache        = make(map[string]IOBase)
)

type VarType int

const (
	VAR_STRING VarType = iota
	VAR_BOOL
	VAR_FLOAT
)

func (t VarType) String() string {
	return [...]string{"string", "bool", "float"}[t]
}

func VarTypeFromStr(t string) VarType {
	return map[string]VarType{
		"string": VAR_STRING,
		"bool":   VAR_BOOL,
		"float":  VAR_FLOAT,
	}[t]
}

func getNameFromId(id string) (ioName, roomName string) {
	for i := range home.Data.Rooms {
		for j := range home.Data.Rooms[i].Items {
			if home.Data.Rooms[i].Items[j].ID == id {
				return home.Data.Rooms[i].Items[j].Name, home.Data.Rooms[i].Name
			}
		}
	}
	return "", ""
}

func getVarType(id string) VarType {
	return VarTypeFromStr(ioCache[id].VarType)
}

func formatStateData(id, state string) interface{} {
	switch getVarType(id) {
	case VAR_BOOL:
		b, err := strconv.ParseBool(state)
		if err != nil {
			log.Printf("Failed to parse boolean state (%v) for id: %s. err = %v\n", state, id, err)
			return ""
		}
		return b
	case VAR_FLOAT:
		f, err := strconv.ParseFloat(state, 64)
		if err != nil {
			log.Printf("Failed to parse float state (%v) for id: %s. err = %v\n", state, id, err)
			return ""
		}
		return f
	}

	return state
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

	loggedin = false

	log.Println("Calaos: Opening :", "ws://"+config.WebSocketServer.Host+":"+strconv.Itoa(config.WebSocketServer.Port)+"/api")

	websocket_client, _, err := websocket.DefaultDialer.Dial("ws://"+config.WebSocketServer.Host+":"+strconv.Itoa(config.WebSocketServer.Port)+"/api", nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer websocket_client.Close()

	done := make(chan struct{})

	log.Println("Calaos: Try login...")
	msg := "{ \"msg\": \"login\", \"msg_id\": \"1\", \"data\": { \"cn_user\": \"" + config.WebSocketServer.User + "\", \"cn_pass\": \"" + config.WebSocketServer.Password + "\" } }"
	if err = websocket_client.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		log.Println("Write message error")
		return
	}

	go func() {

		// Create a new HTTPClient
		c, err := client.NewHTTPClient(client.HTTPConfig{
			Addr: fmt.Sprintf("http://%s:%d", config.InfluxDB.Host, config.InfluxDB.Port),
		})
		if err != nil {
			log.Fatal(err)
		}

		// Create a new point batch
		bp, err := client.NewBatchPoints(client.BatchPointsConfig{
			Database:  config.InfluxDB.Database,
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
					log.Printf("Calaos: LoggedIn")
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
					log.Printf("Calaos: event received")

					var eventMsg CalaosJsonMsgEvent
					err = json.Unmarshal([]byte(message), &eventMsg)
					if err != nil {
						log.Println("error:", err)
					}
					name, room := getNameFromId(eventMsg.Data.Data.ID)

					log.Println("IO:", name, "("+room+")", "State:", eventMsg.Data.Data.State)

					pointKey := fmt.Sprintf("%s - %s (%s)", eventMsg.Data.Data.ID, name, room)
					fields := map[string]interface{}{
						pointKey: formatStateData(eventMsg.Data.Data.ID, eventMsg.Data.Data.State),
					}

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
					log.Printf("Calaos: get_home received")

					err = json.Unmarshal([]byte(message), &home)
					if err != nil {
						log.Println("error:", err)
					}

					//fill cache
					for i := range home.Data.Rooms {
						for j := range home.Data.Rooms[i].Items {
							ioCache[home.Data.Rooms[i].Items[j].ID] = home.Data.Rooms[i].Items[j]

							//Add a point to initial batch
							pointKey := fmt.Sprintf("%s - %s (%s)", home.Data.Rooms[i].Items[j].ID, home.Data.Rooms[i].Items[j].Name, home.Data.Rooms[i].Name)
							fields := map[string]interface{}{
								pointKey: formatStateData(home.Data.Rooms[i].Items[j].ID, home.Data.Rooms[i].Items[j].State),
							}
							pt, err := client.NewPoint(config.Id, nil, fields, time.Now())
							if err != nil {
								log.Fatal(err)
							}

							bp.AddPoint(pt)
						}
					}

					// Write initial batch
					if err := c.Write(bp); err != nil {
						log.Fatal(err)
					}
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
