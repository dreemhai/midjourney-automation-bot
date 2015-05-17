package server

import (
	"gopkg.in/loom.v1/log"
	"sync"
	"time"
)

type Topic struct {
	Name                 string
	Queue                Queue
	Dispatcher           *Dispatcher
	PendingTimeout       time.Duration
	pendingCheckInterval time.Duration
	waitingCh            chan interface{}
	mutex                sync.Mutex
	store                Store
	logger               log.Logger
}

func NewTopic(name string, pendingTimeout time.Duration, store Store) *Topic {
	dispatcher := NewDispatcher(name, store)

	topic := &Topic{
		Name:                 name,
		Queue:                NewLLQueue(),
		Dispatcher:           dispatcher,
		PendingTimeout:       pendingTimeout,
		pendingCheckInterval: 10 * time.Second,
		waitingCh:            make(chan interface{}),
		store:                store,
		logger:               log.New("Topic:" + name),
	}

	return topic
}

func (t *Topic) Init() error {

	//First time, Messages go from Disk to Queue.
	err := t.store.WalkMessage(func(m *Message) error {
		if m.State == MSG_ENQUEUED {
			t.push(m)
		}
		return nil
	})

	go t.msgDispatch()

	return err
}

func (t *Topic) PushMessage(msg *Message) {
	msg.State = MSG_ENQUEUED
	t.store.PutMessage(msg)
	t.push(msg)

	t.logger.Info("Pushed message id:%s", string(msg.ID[:]))
}

func (t *Topic) FinishMessage(id MessageID) error {
	msg, err := t.store.GetMessage(id)
	if err != nil {
		return err
	}

	msg.State = MSG_FINISHED

	err = t.store.PutMessage(msg)
	if err != nil {
		return err
	}

	t.logger.Info("Finished message id:%v", msg.ID)
	return nil
}

func (t *Topic) PushPendingMsgsInWorker(workerId string) {
	pms, err := t.store.GetPendingMsgsInWorker(workerId)
	if err != nil {
		t.logger.Error("PushPendingMsgsInWorker.Get err: %v", err)
		return
	}

	num := 0
	for _, pm := range pms {
		m, err := t.store.GetMessage(pm.MessageID)
		if err != nil {
			t.logger.Error("PushPendingMsgsInWorker.GetMessage err: %v", err)
			continue
		}

		if m.State == MSG_DEQUEUED {
			t.PushMessage(m)
			num++
			t.logger.Info("Message %v is re-pushed to queue", string(m.ID[:]))
		}
	}

	err = t.store.RemovePendingMsgsInWorker(workerId)
	if err != nil {
		t.logger.Error("PushPendingMsgsInWorker.Remove err:%v", err)
	}

	t.logger.Info("Pushed pending msgs:%v", num)
	return
}

func (t *Topic) push(msg *Message) {
	select {
	case t.waitingCh <- msg:
	default:
		t.mutex.Lock()
		defer t.mutex.Unlock()
		err := t.Queue.Push(msg)
		if err != nil {
			//TODO:
			t.logger.Error("push err: %v", err)
		}
	}
}

func (t *Topic) pop() (msg *Message) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if item := t.Queue.Pop(); item != nil {
		msg = item.(*Message)
		msg.State = MSG_DEQUEUED
	} else {
		item := <-t.waitingCh
		msg = item.(*Message)
		msg.State = MSG_DEQUEUED
	}

	return

}

func (t *Topic) msgDispatch() {
	for {
		select {
		case msg := <-t.Dispatcher.msgPushChan:
			t.logger.Debug("dispatcher.push")
			t.PushMessage(msg)
			if t.logger.IsDebug() {
				t.logger.Debug("From dispatcher msg push id:%v", string(msg.ID[:]))
			}
		default:
			t.logger.Debug("pop")
			msg := t.pop()
			if t.logger.IsDebug() {
				t.logger.Debug("From queue msg pop id:%v", string(msg.ID[:]))
			}
			t.Dispatcher.msgPopChan <- msg
		}
	}
}

/*
func (t *Topic) pendingChecker() {

	//st := time.Now()
	st, _ := time.Parse("2006-Jan-02", "2013-Feb-03")

	walkFunc := func(ts *time.Time, id MessageID) error {
		if ts.Add(t.PendingTimeout).Before(time.Now()) {
			m, err := t.store.GetMessage(id)
			if err != nil {
				//TODO: Log
			}

			m.State = MSG_ENQUEUED
			t.store.PutMessage(m)
			t.store.RemovePendingMsgID(ts)
			t.push(m)
		}

		return nil
	}

	ticker := time.NewTicker(t.pendingCheckInterval)
	for {
		select {
		case <-ticker.C:
			ed := st.Add(t.pendingCheckInterval)
			err := t.store.WalkPendingMsgId(&st, &ed, walkFunc)
			if err != nil {
				//TODO: Log

			}
			st = ed
		}
	}

}
*/
