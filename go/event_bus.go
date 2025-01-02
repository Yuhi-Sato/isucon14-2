package main

import (
	"sync"
)

type RideStatusEventData struct {
	Ride   Ride
	Status string
}

type RideStatusEvent struct {
	Data  RideStatusEventData
	Topic string
}

type RideStatusChannel chan RideStatusEvent

type RideStatusChannelSlice []RideStatusChannel

// EventBus stores the information about subscribers interested for // a particular topic
type EventBus struct {
	subscribers map[string]RideStatusChannelSlice
	rm          sync.RWMutex
}

func (eb *EventBus) Subscribe(topic string, ch RideStatusChannel) {
	eb.rm.Lock()
	if prev, found := eb.subscribers[topic]; found {
		eb.subscribers[topic] = append(prev, ch)
	} else {
		eb.subscribers[topic] = append([]RideStatusChannel{}, ch)
	}
	eb.rm.Unlock()
}

func (eb *EventBus) Unsubscribe(topic string) {
	eb.rm.Lock()
	delete(eb.subscribers, topic)
	eb.rm.Unlock()
}

func (eb *EventBus) Publish(topic string, data RideStatusEventData) {
	eb.rm.RLock()
	if chans, found := eb.subscribers[topic]; found {
		// this is done because the slices refer to same array even though they are passed by value
		// thus we are creating a new slice with our elements thus preserve locking correctly.
		channels := append(RideStatusChannelSlice{}, chans...)
		go func(data RideStatusEvent, RideStatusChannelSlices RideStatusChannelSlice) {
			for _, ch := range RideStatusChannelSlices {
				ch <- data
			}
		}(RideStatusEvent{Data: data, Topic: topic}, channels)
	}
	eb.rm.RUnlock()
}

var eb = &EventBus{
	subscribers: map[string]RideStatusChannelSlice{},
}
