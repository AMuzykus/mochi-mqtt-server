// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2022 mochi-mqtt, mochi-co
// SPDX-FileContributor: werbenhu

package pebble

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	pebbledb "github.com/cockroachdb/pebble"
	mqtt "github.com/AMuzykus/mochi-mqtt-server/v2"
	"github.com/AMuzykus/mochi-mqtt-server/v2/hooks/storage"
	"github.com/AMuzykus/mochi-mqtt-server/v2/packets"
	"github.com/AMuzykus/mochi-mqtt-server/v2/system"
	"github.com/stretchr/testify/require"
)

var (
	logger = slog.New(slog.NewTextHandler(os.Stdout, nil))

	client = &mqtt.Client{
		ID: "test",
		Net: mqtt.ClientConnection{
			Remote:   "test.addr",
			Listener: "listener",
		},
		Properties: mqtt.ClientProperties{
			Username: []byte("username"),
			Clean:    false,
		},
	}

	pkf = packets.Packet{Filters: packets.Subscriptions{{Filter: "a/b/c"}}}
)

func teardown(t *testing.T, path string, h *Hook) {
	_ = h.Stop()
	err := os.RemoveAll("./" + strings.Replace(path, "..", "", -1))
	require.NoError(t, err)
}

func TestKeyUpperBound(t *testing.T) {
	// Test case 1: Non-nil case
	input1 := []byte{97, 98, 99} // "abc"
	require.NotNil(t, keyUpperBound(input1))

	// Test case 2: All bytes are 255
	input2 := []byte{255, 255, 255}
	require.Nil(t, keyUpperBound(input2))

	// Test case 3: Empty slice
	input3 := []byte{}
	require.Nil(t, keyUpperBound(input3))

	// Test case 4: Nil case
	input4 := []byte{255, 255, 255}
	require.Nil(t, keyUpperBound(input4))
}

func TestClientKey(t *testing.T) {
	k := clientKey(&mqtt.Client{ID: "cl1"})
	require.Equal(t, storage.ClientKey+"_cl1", k)
}

func TestSubscriptionKey(t *testing.T) {
	k := subscriptionKey(&mqtt.Client{ID: "cl1"}, "a/b/c")
	require.Equal(t, storage.SubscriptionKey+"_cl1:a/b/c", k)
}

func TestRetainedKey(t *testing.T) {
	k := retainedKey("a/b/c")
	require.Equal(t, storage.RetainedKey+"_a/b/c", k)
}

func TestInflightKey(t *testing.T) {
	k := inflightKey(&mqtt.Client{ID: "cl1"}, packets.Packet{PacketID: 1})
	require.Equal(t, storage.InflightKey+"_cl1:1", k)
}

func TestSysInfoKey(t *testing.T) {
	require.Equal(t, storage.SysInfoKey, sysInfoKey())
}

func TestID(t *testing.T) {
	h := new(Hook)
	require.Equal(t, "pebble-db", h.ID())
}

func TestProvides(t *testing.T) {
	h := new(Hook)
	require.True(t, h.Provides(mqtt.OnSessionEstablished))
	require.True(t, h.Provides(mqtt.OnDisconnect))
	require.True(t, h.Provides(mqtt.OnSubscribed))
	require.True(t, h.Provides(mqtt.OnUnsubscribed))
	require.True(t, h.Provides(mqtt.OnRetainMessage))
	require.True(t, h.Provides(mqtt.OnQosPublish))
	require.True(t, h.Provides(mqtt.OnQosComplete))
	require.True(t, h.Provides(mqtt.OnQosDropped))
	require.True(t, h.Provides(mqtt.OnSysInfoTick))
	require.True(t, h.Provides(mqtt.StoredClients))
	require.True(t, h.Provides(mqtt.StoredInflightMessages))
	require.True(t, h.Provides(mqtt.StoredRetainedMessages))
	require.True(t, h.Provides(mqtt.StoredSubscriptions))
	require.True(t, h.Provides(mqtt.StoredSysInfo))
	require.False(t, h.Provides(mqtt.OnACLCheck))
	require.False(t, h.Provides(mqtt.OnConnectAuthenticate))
}

func TestInitBadConfig(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)

	err := h.Init(map[string]any{})
	require.Error(t, err)
}

func TestInitErr(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)

	err := h.Init(&Options{
		Options: &pebbledb.Options{
			ReadOnly: true,
		},
	})
	require.Error(t, err)
}

func TestInitUseDefaults(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	require.Equal(t, defaultDbFile, h.config.Path)
}

func TestOnSessionEstablishedThenOnDisconnect(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	h.OnSessionEstablished(client, packets.Packet{})

	r := new(storage.Client)
	err = h.getKv(clientKey(client), r)
	require.NoError(t, err)
	require.Equal(t, client.ID, r.ID)
	require.Equal(t, client.Properties.Username, r.Username)
	require.Equal(t, client.Properties.Clean, r.Clean)
	require.Equal(t, client.Net.Remote, r.Remote)
	require.Equal(t, client.Net.Listener, r.Listener)
	require.NotSame(t, client, r)

	h.OnDisconnect(client, nil, false)
	r2 := new(storage.Client)
	err = h.getKv(clientKey(client), r2)
	require.NoError(t, err)
	require.Equal(t, client.ID, r.ID)

	h.OnDisconnect(client, nil, true)
	r3 := new(storage.Client)
	err = h.getKv(clientKey(client), r3)
	require.Error(t, err)
	require.ErrorIs(t, pebbledb.ErrNotFound, err)
	require.Empty(t, r3.ID)

}

func TestOnClientExpired(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	cl := &mqtt.Client{ID: "cl1"}
	clientKey := clientKey(cl)

	err = h.setKv(clientKey, &storage.Client{ID: cl.ID})
	require.NoError(t, err)

	r := new(storage.Client)
	err = h.getKv(clientKey, r)
	require.NoError(t, err)
	require.Equal(t, cl.ID, r.ID)

	h.OnClientExpired(cl)
	err = h.getKv(clientKey, r)
	require.Error(t, err)
	require.ErrorIs(t, err, pebbledb.ErrNotFound)
}

func TestOnClientExpiredNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnClientExpired(client)
}

func TestOnClientExpiredClosedDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	teardown(t, h.config.Path, h)
	h.OnClientExpired(client)
}

func TestOnSessionEstablishedNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnSessionEstablished(client, packets.Packet{})
}

func TestOnSessionEstablishedClosedDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	teardown(t, h.config.Path, h)
	h.OnSessionEstablished(client, packets.Packet{})
}

func TestOnWillSent(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	c1 := client
	c1.Properties.Will.Flag = 1
	h.OnWillSent(c1, packets.Packet{})

	r := new(storage.Client)
	err = h.getKv(clientKey(client), r)
	require.NoError(t, err)

	require.Equal(t, uint32(1), r.Will.Flag)
	require.NotSame(t, client, r)
}

func TestOnDisconnectNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnDisconnect(client, nil, false)
}

func TestOnDisconnectClosedDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	teardown(t, h.config.Path, h)
	h.OnDisconnect(client, nil, false)
}

func TestOnDisconnectSessionTakenOver(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)

	testClient := &mqtt.Client{
		ID: "test",
		Net: mqtt.ClientConnection{
			Remote:   "test.addr",
			Listener: "listener",
		},
		Properties: mqtt.ClientProperties{
			Username: []byte("username"),
			Clean:    false,
		},
	}

	testClient.Stop(packets.ErrSessionTakenOver)
	h.OnDisconnect(testClient, nil, true)
	teardown(t, h.config.Path, h)
}

func TestOnSubscribedThenOnUnsubscribed(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	h.OnSubscribed(client, pkf, []byte{0})
	r := new(storage.Subscription)

	err = h.getKv(subscriptionKey(client, pkf.Filters[0].Filter), r)
	require.NoError(t, err)
	require.Equal(t, client.ID, r.Client)
	require.Equal(t, pkf.Filters[0].Filter, r.Filter)
	require.Equal(t, byte(0), r.Qos)

	h.OnUnsubscribed(client, pkf)
	err = h.getKv(subscriptionKey(client, pkf.Filters[0].Filter), r)
	require.Error(t, err)
	require.Equal(t, pebbledb.ErrNotFound, err)
}

func TestOnSubscribedNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnSubscribed(client, pkf, []byte{0})
}

func TestOnSubscribedClosedDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	teardown(t, h.config.Path, h)
	h.OnSubscribed(client, pkf, []byte{0})
}

func TestOnUnsubscribedNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnUnsubscribed(client, pkf)
}

func TestOnUnsubscribedClosedDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	teardown(t, h.config.Path, h)
	h.OnUnsubscribed(client, pkf)
}

func TestOnRetainMessageThenUnset(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	pk := packets.Packet{
		FixedHeader: packets.FixedHeader{
			Retain: true,
		},
		Payload:   []byte("hello"),
		TopicName: "a/b/c",
	}

	h.OnRetainMessage(client, pk, 1)

	r := new(storage.Message)
	err = h.getKv(retainedKey(pk.TopicName), r)
	require.NoError(t, err)
	require.Equal(t, pk.TopicName, r.TopicName)
	require.Equal(t, pk.Payload, r.Payload)

	h.OnRetainMessage(client, pk, -1)
	err = h.getKv(retainedKey(pk.TopicName), r)
	require.Error(t, err)
	require.ErrorIs(t, err, pebbledb.ErrNotFound)

	// coverage: delete deleted
	h.OnRetainMessage(client, pk, -1)
	err = h.getKv(retainedKey(pk.TopicName), r)
	require.Error(t, err)
	require.ErrorIs(t, err, pebbledb.ErrNotFound)
}

func TestOnRetainedExpired(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	m := &storage.Message{
		ID:        retainedKey("a/b/c"),
		T:         storage.RetainedKey,
		TopicName: "a/b/c",
	}

	err = h.setKv(m.ID, m)
	require.NoError(t, err)

	r := new(storage.Message)
	err = h.getKv(m.ID, r)
	require.NoError(t, err)
	require.Equal(t, m.TopicName, r.TopicName)

	h.OnRetainedExpired(m.TopicName)
	err = h.getKv(m.ID, r)
	require.Error(t, err)
	require.ErrorIs(t, err, pebbledb.ErrNotFound)
}

func TestOnRetainExpiredNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnRetainedExpired("a/b/c")
}

func TestOnRetainExpiredClosedDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	teardown(t, h.config.Path, h)
	h.OnRetainedExpired("a/b/c")
}

func TestOnRetainMessageNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnRetainMessage(client, packets.Packet{}, 0)
}

func TestOnRetainMessageClosedDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	teardown(t, h.config.Path, h)
	h.OnRetainMessage(client, packets.Packet{}, 0)
}

func TestOnQosPublishThenQOSComplete(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	pk := packets.Packet{
		FixedHeader: packets.FixedHeader{
			Retain: true,
			Qos:    2,
		},
		Payload:   []byte("hello"),
		TopicName: "a/b/c",
	}

	h.OnQosPublish(client, pk, time.Now().Unix(), 0)

	r := new(storage.Message)
	err = h.getKv(inflightKey(client, pk), r)
	require.NoError(t, err)
	require.Equal(t, pk.TopicName, r.TopicName)
	require.Equal(t, pk.Payload, r.Payload)

	// ensure dates are properly saved
	require.True(t, r.Sent > 0)
	require.True(t, time.Now().Unix()-1 < r.Sent)

	// OnQosDropped is a passthrough to OnQosComplete here
	h.OnQosDropped(client, pk)
	err = h.getKv(inflightKey(client, pk), r)
	require.Error(t, err)
	require.ErrorIs(t, err, pebbledb.ErrNotFound)
}

func TestOnQosPublishNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnQosPublish(client, packets.Packet{}, time.Now().Unix(), 0)
}

func TestOnQosPublishClosedDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	teardown(t, h.config.Path, h)
	h.OnQosPublish(client, packets.Packet{}, time.Now().Unix(), 0)
}

func TestOnQosCompleteNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnQosComplete(client, packets.Packet{})
}

func TestOnQosCompleteClosedDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	teardown(t, h.config.Path, h)
	h.OnQosComplete(client, packets.Packet{})
}

func TestOnQosDroppedNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnQosDropped(client, packets.Packet{})
}

func TestOnSysInfoTick(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	info := &system.Info{
		Version:       "2.0.0",
		BytesReceived: 100,
	}

	h.OnSysInfoTick(info)

	r := new(storage.SystemInfo)
	err = h.getKv(storage.SysInfoKey, r)
	require.NoError(t, err)
	require.Equal(t, info.Version, r.Version)
	require.Equal(t, info.BytesReceived, r.BytesReceived)
	require.NotSame(t, info, r)
}

func TestOnSysInfoTickNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.OnSysInfoTick(new(system.Info))
}

func TestOnSysInfoTickClosedDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	teardown(t, h.config.Path, h)
	h.OnSysInfoTick(new(system.Info))
}

func TestStoredClients(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	// populate with clients
	err = h.setKv(storage.ClientKey+"_cl1", &storage.Client{ID: "cl1"})
	require.NoError(t, err)

	err = h.setKv(storage.ClientKey+"_cl2", &storage.Client{ID: "cl2"})
	require.NoError(t, err)

	err = h.setKv(storage.ClientKey+"_cl3", &storage.Client{ID: "cl3"})
	require.NoError(t, err)

	r, err := h.StoredClients()
	require.NoError(t, err)
	require.Len(t, r, 3)
	require.Equal(t, "cl1", r[0].ID)
	require.Equal(t, "cl2", r[1].ID)
	require.Equal(t, "cl3", r[2].ID)
}

func TestStoredClientsNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	v, err := h.StoredClients()
	require.Empty(t, v)
	require.NoError(t, err)
}

func TestStoredSubscriptions(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	// populate with subscriptions
	err = h.setKv(storage.SubscriptionKey+"_sub1", &storage.Subscription{ID: "sub1"})
	require.NoError(t, err)

	err = h.setKv(storage.SubscriptionKey+"_sub2", &storage.Subscription{ID: "sub2"})
	require.NoError(t, err)

	err = h.setKv(storage.SubscriptionKey+"_sub3", &storage.Subscription{ID: "sub3"})
	require.NoError(t, err)

	r, err := h.StoredSubscriptions()
	require.NoError(t, err)
	require.Len(t, r, 3)
	require.Equal(t, "sub1", r[0].ID)
	require.Equal(t, "sub2", r[1].ID)
	require.Equal(t, "sub3", r[2].ID)
}

func TestStoredSubscriptionsNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	v, err := h.StoredSubscriptions()
	require.Empty(t, v)
	require.NoError(t, err)
}

func TestStoredRetainedMessages(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	// populate with messages
	err = h.setKv(storage.RetainedKey+"_m1", &storage.Message{ID: "m1"})
	require.NoError(t, err)

	err = h.setKv(storage.RetainedKey+"_m2", &storage.Message{ID: "m2"})
	require.NoError(t, err)

	err = h.setKv(storage.RetainedKey+"_m3", &storage.Message{ID: "m3"})
	require.NoError(t, err)

	err = h.setKv(storage.InflightKey+"_i3", &storage.Message{ID: "i3"})
	require.NoError(t, err)

	r, err := h.StoredRetainedMessages()
	require.NoError(t, err)
	require.Len(t, r, 3)
	require.Equal(t, "m1", r[0].ID)
	require.Equal(t, "m2", r[1].ID)
	require.Equal(t, "m3", r[2].ID)
}

func TestStoredRetainedMessagesNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	v, err := h.StoredRetainedMessages()
	require.Empty(t, v)
	require.NoError(t, err)
}

func TestStoredInflightMessages(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	// populate with messages
	err = h.setKv(storage.InflightKey+"_i1", &storage.Message{ID: "i1"})
	require.NoError(t, err)

	err = h.setKv(storage.InflightKey+"_i2", &storage.Message{ID: "i2"})
	require.NoError(t, err)

	err = h.setKv(storage.InflightKey+"_i3", &storage.Message{ID: "i3"})
	require.NoError(t, err)

	err = h.setKv(storage.RetainedKey+"_m1", &storage.Message{ID: "m1"})
	require.NoError(t, err)

	r, err := h.StoredInflightMessages()
	require.NoError(t, err)
	require.Len(t, r, 3)
	require.Equal(t, "i1", r[0].ID)
	require.Equal(t, "i2", r[1].ID)
	require.Equal(t, "i3", r[2].ID)
}

func TestStoredInflightMessagesNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	v, err := h.StoredInflightMessages()
	require.Empty(t, v)
	require.NoError(t, err)
}

func TestStoredSysInfo(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	err := h.Init(nil)
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	r, err := h.StoredSysInfo()
	require.NoError(t, err)

	// populate with messages
	err = h.setKv(storage.SysInfoKey, &storage.SystemInfo{
		ID: storage.SysInfoKey,
		Info: system.Info{
			Version: "2.0.0",
		},
		T: storage.SysInfoKey,
	})
	require.NoError(t, err)

	r, err = h.StoredSysInfo()
	require.NoError(t, err)
	require.Equal(t, "2.0.0", r.Info.Version)
}

func TestStoredSysInfoNoDB(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)
	v, err := h.StoredSysInfo()
	require.Empty(t, v)
	require.NoError(t, err)
}

func TestErrorf(t *testing.T) {
	// coverage: one day check log hook
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.Errorf("test", 1, 2, 3)
}

func TestWarningf(t *testing.T) {
	// coverage: one day check log hook
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.Warningf("test", 1, 2, 3)
}

func TestInfof(t *testing.T) {
	// coverage: one day check log hook
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.Infof("test", 1, 2, 3)
}

func TestDebugf(t *testing.T) {
	// coverage: one day check log hook
	h := new(Hook)
	h.SetOpts(logger, nil)
	h.Debugf("test", 1, 2, 3)
}

func TestGetSetDelKv(t *testing.T) {
	opts := []struct {
		name string
		opt  *Options
	}{
		{
			name: "NoSync",
			opt: &Options{
				Mode: NoSync,
			},
		},
		{
			name: "Sync",
			opt: &Options{
				Mode: Sync,
			},
		},
	}
	for _, tt := range opts {
		t.Run(tt.name, func(t *testing.T) {
			h := new(Hook)
			h.SetOpts(logger, nil)
			err := h.Init(tt.opt)
			defer teardown(t, h.config.Path, h)
			require.NoError(t, err)

			err = h.setKv("testKey", &storage.Client{ID: "testId"})
			require.NoError(t, err)

			var obj storage.Client
			err = h.getKv("testKey", &obj)
			require.NoError(t, err)

			err = h.delKv("testKey")
			require.NoError(t, err)

			err = h.getKv("testKey", &obj)
			require.Error(t, err)
			require.ErrorIs(t, pebbledb.ErrNotFound, err)
		})
	}
}

func TestGetSetDelKvErr(t *testing.T) {
	h := new(Hook)
	h.SetOpts(logger, nil)

	err := h.Init(&Options{
		Mode: Sync,
	})
	require.NoError(t, err)

	err = h.setKv("testKey", &storage.Client{ID: "testId"})
	require.NoError(t, err)
	h.Stop()

	h = new(Hook)
	h.SetOpts(logger, nil)

	err = h.Init(&Options{
		Mode: Sync,
		Options: &pebbledb.Options{
			ReadOnly: true,
		},
	})
	require.NoError(t, err)
	defer teardown(t, h.config.Path, h)

	err = h.setKv("testKey", &storage.Client{ID: "testId"})
	require.Error(t, err)

	err = h.delKv("testKey")
	require.Error(t, err)
}
