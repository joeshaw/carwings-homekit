package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/brutella/hc"
	"github.com/brutella/hc/accessory"
	"github.com/brutella/hc/characteristic"
	"github.com/brutella/hc/service"
	"github.com/joeshaw/carwings"
)

type ChargeService struct {
	*service.Switch
	BatteryLevel  *characteristic.BatteryLevel
	ChargingState *characteristic.ChargingState
}

func newChargeService() *ChargeService {
	svc := &ChargeService{
		Switch:        service.NewSwitch(),
		BatteryLevel:  characteristic.NewBatteryLevel(),
		ChargingState: characteristic.NewChargingState(),
	}

	svc.AddCharacteristic(svc.BatteryLevel.Characteristic)
	svc.AddCharacteristic(svc.ChargingState.Characteristic)

	return svc
}

type Leaf struct {
	sess *carwings.Session

	battUpdate chan chan struct{}
	hvacUpdate chan chan struct{}

	acc       *accessory.Accessory
	battSvc   *service.BatteryService
	hvacSvc   *service.Fan
	chargeSvc *ChargeService
}

type Config struct {
	// Storage path for information about the HomeKit accessory.
	// Defaults to ~/.homecontrol
	StoragePath string `json:"storage_path"`

	// Carwings username (email address)
	Username string `json:"username"`

	// Carwings password
	Password string `json:"password"`

	// Carwings region.  Defaults to "NNA" (United States)
	Region string `json:"region"`

	// Accessory name.  Defaults to "Leaf"
	AccessoryName string `json:"accessory_name"`

	// HomeKit PIN.  Defaults to 00102003.
	HomekitPIN string `json:"homekit_pin"`
}

func main() {
	var configFile string

	flag.StringVar(&configFile, "config", "config.json", "config file")
	flag.Parse()

	// Default values
	config := Config{
		StoragePath:   filepath.Join(os.Getenv("HOME"), ".homecontrol", "carwings"),
		Region:        "NNA",
		AccessoryName: "Car",
		HomekitPIN:    "00102003",
	}

	f, err := os.Open(configFile)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	if err := dec.Decode(&config); err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Println("Connecting to Carwings service")
	s, err := carwings.Connect(config.Username, config.Password, config.Region)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Connected to Carwings")

	info := accessory.Info{
		Name:         config.AccessoryName,
		Manufacturer: "Nissan",
		Model:        "Leaf",
		SerialNumber: s.VIN,
	}

	leaf := &Leaf{
		sess:       s,
		battUpdate: make(chan chan struct{}),
		hvacUpdate: make(chan chan struct{}),
		acc:        accessory.New(info, accessory.TypeOther),
		battSvc:    service.NewBatteryService(),
		chargeSvc:  newChargeService(),
		hvacSvc:    service.NewFan(),
	}

	n := characteristic.NewName()
	n.SetValue("Battery")
	leaf.battSvc.AddCharacteristic(n.Characteristic)

	n = characteristic.NewName()
	n.SetValue("Charging")
	leaf.chargeSvc.AddCharacteristic(n.Characteristic)

	n = characteristic.NewName()
	n.SetValue("Climate Control")
	leaf.hvacSvc.AddCharacteristic(n.Characteristic)

	leaf.acc.AddService(leaf.battSvc.Service)
	leaf.acc.AddService(leaf.hvacSvc.Service)
	leaf.acc.AddService(leaf.chargeSvc.Service)

	leaf.chargeSvc.On.OnValueRemoteUpdate(func(on bool) {
		if !on {
			log.Println("Charging cannot be switched off")
			leaf.chargeSvc.On.SetValue(true)
			return
		}

		// Contacting Carwings takes too long; run this in a goroutine
		go func() {
			log.Println("Sending charging request")
			if err := leaf.sess.ChargingRequest(); err != nil {
				log.Printf("Error requesting charging: %v", err)
				return
			}

			// Request a battery status update
			leaf.battUpdate <- make(chan struct{})
		}()
	})

	leaf.hvacSvc.On.OnValueRemoteUpdate(func(on bool) {
		// Contacting Carwings takes too long; run this in a goroutine
		go func() {
			if on {
				log.Println("Sending request to turn on climate control")
				key, err := leaf.sess.ClimateOnRequest()
				if err != nil {
					log.Printf("Error requesting climate on: %v", err)
					return
				}

				if err := waitOnKey(ctx, key, leaf.sess.CheckClimateOnRequest); err != nil {
					log.Printf("Error requesting climate on (%s): %v", key, err)
					return
				}
			} else {
				log.Println("Sending request to turn off climate control")
				key, err := leaf.sess.ClimateOffRequest()
				if err != nil {
					log.Printf("Error requesting climate off: %v", err)
					return
				}

				if err := waitOnKey(ctx, key, leaf.sess.CheckClimateOffRequest); err != nil {
					log.Printf("Error requesting climate off (%s): %v", key, err)
					return
				}
			}

			// Request a climate status update
			leaf.hvacUpdate <- make(chan struct{})
		}()
	})

	hcConfig := hc.Config{
		Pin:         config.HomekitPIN,
		StoragePath: filepath.Join(config.StoragePath, info.Name),
	}
	t, err := hc.NewIPTransport(hcConfig, leaf.acc)
	if err != nil {
		log.Fatal(err)
	}

	hc.OnTermination(func() {
		cancel()
		<-t.Stop()
	})

	go updateBattery(ctx, leaf)
	go updateClimate(ctx, leaf)

	// Update battery and climate status every 15m
	go func() {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))

		battJitter := time.Duration(r.Intn(240)-120) * time.Second
		hvacJitter := time.Duration(r.Intn(240)-120) * time.Second

		battTicker := time.NewTicker(15*time.Minute + battJitter)
		defer battTicker.Stop()

		hvacTicker := time.NewTicker(15*time.Minute + hvacJitter)
		defer hvacTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-battTicker.C:
				leaf.battUpdate <- make(chan struct{})
			case <-hvacTicker.C:
				leaf.hvacUpdate <- make(chan struct{})
			}
		}
	}()

	// And update them initially, and wait for them to finish
	// before exposing the accessory to HomeKit.
	ch1, ch2 := make(chan struct{}), make(chan struct{})
	leaf.battUpdate <- ch1
	leaf.hvacUpdate <- ch2
	<-ch1
	<-ch2

	log.Println("Starting transport...")
	t.Start()
}

func updateBattery(ctx context.Context, leaf *Leaf) {
	log.Println("Entering battery update loop")
	defer log.Println("Exited battery update loop")

	for {
		var ch chan struct{}

		select {
		case <-ctx.Done():
			return
		case ch = <-leaf.battUpdate:
		}

		log.Println("Updating battery information")

		key, err := leaf.sess.UpdateStatus()
		if err != nil {
			log.Printf("Error getting battery status: %v", err)
			close(ch)
			continue
		}

		if err := waitOnKey(ctx, key, leaf.sess.CheckUpdate); err != nil {
			log.Printf("Error updating battery status (%s): %v", key, err)
			close(ch)
			continue
		}

		bs, err := leaf.sess.BatteryStatus()
		if err != nil {
			log.Printf("Error getting battery status: %v", err)
			close(ch)
			continue
		}

		switch bs.ChargingStatus {
		case carwings.NotCharging, carwings.NormalCharging, carwings.RapidlyCharging:
			leaf.battSvc.BatteryLevel.SetValue(bs.StateOfCharge)
			leaf.chargeSvc.BatteryLevel.SetValue(bs.StateOfCharge)

			// Ideally we'd only set this if the Leaf's low
			// battery warning was set, but the Carwings API
			// doesn't give that to us.  So let's just say 20%.
			lowBatt := characteristic.StatusLowBatteryBatteryLevelNormal
			if bs.StateOfCharge <= 20 {
				lowBatt = characteristic.StatusLowBatteryBatteryLevelLow
			}
			leaf.battSvc.StatusLowBattery.SetValue(lowBatt)

			status := characteristic.ChargingStateNotCharging
			if bs.ChargingStatus == carwings.NormalCharging || bs.ChargingStatus == carwings.RapidlyCharging {
				status = characteristic.ChargingStateCharging
			}
			leaf.battSvc.ChargingState.SetValue(status)
			leaf.chargeSvc.ChargingState.SetValue(status)
			leaf.chargeSvc.On.SetValue(status == characteristic.ChargingStateCharging)

			log.Printf("Battery info update complete: %d%%, %s", bs.StateOfCharge, bs.ChargingStatus)

		default:
			log.Printf("Invalid battery info state: %d%%, %s", bs.StateOfCharge, bs.ChargingStatus)
		}

		close(ch)
	}
}

func updateClimate(ctx context.Context, leaf *Leaf) {
	log.Println("Entering climate control update loop")
	defer log.Println("Exited climate control update loop")

	for {
		var ch chan struct{}
		select {
		case <-ctx.Done():
			return
		case ch = <-leaf.hvacUpdate:
		}

		log.Println("Updating climate control information")

		cs, err := leaf.sess.ClimateControlStatus()
		if err != nil {
			log.Printf("Error getting climate control status: %v", err)

			// If we get an error, assume the climate control is off
			leaf.hvacSvc.On.SetValue(false)

			close(ch)
			continue
		}

		leaf.hvacSvc.On.SetValue(cs.Running)

		log.Printf("Climate control info update complete: running %t", cs.Running)
		close(ch)
	}
}

func waitOnKey(ctx context.Context, key string, fn func(string) (bool, error)) error {
	interval := 0 * time.Second
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		interval = 5 * time.Second

		if done, err := fn(key); done {
			return nil
		} else if err != nil {
			return err
		}
	}
}
