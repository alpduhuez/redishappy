package sentinel

import (
	"errors"
	"github.com/mdevilliers/redishappy/services/logger"
	"github.com/mdevilliers/redishappy/services/redis"
	"github.com/mdevilliers/redishappy/types"
	"strconv"
	"strings"
	"time"
)

type Monitor struct {
	client   *redis.PubSubClient
	channel  chan redis.RedisPubSubReply
	manager  Manager
	sentinel types.Sentinel
}

func NewMonitor(sentinel types.Sentinel, manager Manager, redisConnection redis.RedisConnection) (*Monitor, error) {

	uri := sentinel.GetLocation()
	logger.Info.Printf("Connecting to sentinel@%s", uri)

	channel := make(chan redis.RedisPubSubReply)
	client, err := redis.NewPubSubClient(uri, channel, redisConnection)

	if err != nil {
		return nil, err
	}

	monitor := &Monitor{client: client, channel: channel, manager: manager, sentinel: sentinel}
	return monitor, nil
}

func (m *Monitor) StartMonitoringMasterEvents(switchmasterchannel chan types.MasterSwitchedEvent) error {

	keys := []string{"+switch-master", "+sentinel", "+slave-reconf-done"}
	err := m.client.Start(keys)

	if err != nil {
		return err
	}

	go m.loop(switchmasterchannel)

	return nil
}

func (m *Monitor) loop(switchmasterchannel chan types.MasterSwitchedEvent) {
	for {
		select {
		case message := <-m.channel:
			err := m.dealWithSentinelMessage(message, switchmasterchannel)
			if err != nil {
				break
			}

		case <-time.After(time.Duration(1) * time.Second):
			m.manager.Notify(&SentinelPing{Sentinel: m.sentinel})
		}
	}
}

func (m *Monitor) dealWithSentinelMessage(message redis.RedisPubSubReply, switchmasterchannel chan types.MasterSwitchedEvent) error {

	if message.Timeout() {
		return errors.New("Timeout")
	}
	if message.Err() != nil {
		m.manager.Notify(&SentinelLost{Sentinel: m.sentinel})
		logger.Info.Printf("Subscription Message : Channel : Error %s \n", message.Err())
		return errors.New("Sentinel Lost")
	}

	channel := message.Channel()
	logger.Info.Printf("Channel : %s", channel)
	if channel == "+switch-master" {
		logger.Info.Printf("Subscription Message : Channel : %s : %s\n", message.Channel(), message.Message())

		event := parseSwitchMasterMessage(message.Message())
		switchmasterchannel <- event
		return nil
	}
	if channel == "+sentinel" {

		host, port := parseInstanceDetailsForIpAndPortMessage(message.Message())
		m.manager.Notify(&SentinelAdded{Sentinel: types.Sentinel{Host: host, Port: port}})
		return nil
	}

	logger.Error.Printf("Subscription Message : Unknown Channel : %s \n", channel)
	return nil
}

func parseInstanceDetailsForIpAndPortMessage(message string) (string, int) {
	//<instance-type> <name> <ip> <port> @ <master-name> <master-ip> <master-port>
	bits := strings.Split(message, " ")
	port, _ := strconv.Atoi(bits[3])
	return bits[2], port
}

func parseSwitchMasterMessage(message string) types.MasterSwitchedEvent {
	bits := strings.Split(message, " ")

	oldmasterport, _ := strconv.Atoi(bits[2])
	newmasterport, _ := strconv.Atoi(bits[4])

	return types.MasterSwitchedEvent{Name: bits[0], OldMasterIp: bits[1], OldMasterPort: oldmasterport, NewMasterIp: bits[3], NewMasterPort: newmasterport}
}
