package fnsq

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nsqio/go-nsq"
)

type manage struct {
	ctx        context.Context
	cancel     context.CancelFunc
	config     Config
	nsqConfig  *nsq.Config
	workerLock sync.RWMutex
	workers    map[string]Work
	workChan   *WorkChan
}

func (m *manage) NsqConfig() *nsq.Config {
	return m.nsqConfig
}

func (m *manage) SetNSQConfig(nsqConfig *nsq.Config) {
	m.nsqConfig = nsqConfig
}

func (m *manage) RegistryWorker(work Work) Work {
	m.workerLock.Lock()
	m.workers[work.Topic()] = work
	m.workerLock.Unlock()
	return work
}

func (m *manage) Work(topic string) (Work, bool) {
	m.workerLock.RLock()
	work, exist := m.workers[topic]
	m.workerLock.RUnlock()
	return work, exist
}

func (m *manage) DestroyWork(work Work) {
	m.workerLock.Lock()
	delete(m.workers, work.Topic())
	m.workerLock.Unlock()
	work.Stop()
}

func (m *manage) Works() []Work {
	var works []Work
	m.workerLock.Lock()
	for i := range m.workers {
		works = append(works, m.workers[i])
	}
	m.workerLock.Unlock()
	return works
}

func (m *manage) consumeWorker(work Work) error {
	consumer, err := work.Consumer(m.nsqConfig)
	if err != nil {
		return err
	}
	for {
		select {
		case <-m.ctx.Done():
		default:
			err = consumer.ConnectToNSQLookupd(m.config.ConsumeAddr)
			if err != nil {
				continue
			}
		}

	}

}

func (m *manage) PublishWork(work Work) {
	m.workChan.In <- work
}

func (m *manage) StartRegisterServer(channel string) Work {
	work, b := m.Work(m.config.RegisterName)
	if b {
		return work
	}
	work = NewConsumeWork(m.config.RegisterName, channel)
	m.ConsumeWork(work, 0)
	return work
}

func (m *manage) ConsumeWork(work Work, delay int) {
	m.RegistryWorker(work)

	go func(delay int) {
		if delay != 0 {
			t := time.NewTimer(time.Duration(delay) * time.Second)
			defer t.Stop()
			select {
			case <-t.C:
				m.consumeWorker(work)
			}
		} else {
			m.consumeWorker(work)
		}
	}(delay)

}

func (m *manage) Start() {
	go m.produceWorker()
}

func (m *manage) Stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	for _, w := range m.Works() {
		w.Stop()
	}
}

func (m *manage) Wait() {
	<-m.ctx.Done()
}

func (m *manage) RegisterClient(channel string, message WorkMessage) Work {
	m.PublishWork(NewPublishWork(m.config.RegisterName, message))

	work := NewConsumeWork(message.Topic, channel)
	m.ConsumeWork(work, 5)
	return work
}

func (m *manage) produceWorker() error {
	producer, err := nsq.NewProducer(m.config.ProducerAddr, m.nsqConfig)
	if err != nil {
		return err
	}
	defer producer.Stop()
	var work Work
	for {
		errPing := producer.Ping()
		if errPing != nil {
			fmt.Printf("faile ping:%+v\n", errPing)
			break
		}
		select {
		case <-m.ctx.Done():
			return m.ctx.Err()
		case work = <-m.workChan.Out:
			fmt.Printf("send work:%+v\n", work)
			err = producer.Publish(work.Topic(), work.Data())
			if err != nil {
				fmt.Println("err", err)
				continue
			}
		}
	}
	return nil
}

func initManage(ctx context.Context, config Config) Manager {
	ctx, cancel := context.WithCancel(ctx)
	return &manage{
		ctx:       ctx,
		cancel:    cancel,
		config:    config,
		nsqConfig: nsq.NewConfig(),
		workers:   make(map[string]Work, 1),
		workChan:  NewWorkChan(5),
	}
}

func NewManager(ctx context.Context, config Config) Manager {
	return initManage(ctx, config)
}

type Manager interface {
	NsqConfig() *nsq.Config
	SetNSQConfig(nsqConfig *nsq.Config)
	RegistryWorker(work Work) Work
	Work(topic string) (Work, bool)
	DestroyWork(work Work)
	Works() []Work
	PublishWork(work Work)
	StartRegisterServer(channel string) Work
	RegisterClient(channel string, message WorkMessage) Work
	ConsumeWork(work Work, delay int)
	Start()
	Stop()
	Wait()
}
