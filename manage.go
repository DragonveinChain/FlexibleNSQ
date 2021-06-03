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
	workers    map[string]Worker
	workChan   *WorkerChan
}

func (m *manage) NsqConfig() *nsq.Config {
	return m.nsqConfig
}

func (m *manage) SetNSQConfig(nsqConfig *nsq.Config) {
	m.nsqConfig = nsqConfig
}

func (m *manage) addWorker(worker Worker) {
	m.workerLock.Lock()
	m.workers[worker.Topic()] = worker
	m.workerLock.Unlock()
}

func (m *manage) RegistryWorker(work Worker) (Worker, bool) {
	worker, b := m.Worker(work.Topic())
	if b {
		return worker, false
	}
	m.addWorker(work)
	return work, true
}

func (m *manage) Worker(topic string) (Worker, bool) {
	m.workerLock.RLock()
	work, exist := m.workers[topic]
	m.workerLock.RUnlock()
	return work, exist
}

func (m *manage) DestroyWorker(topic string) bool {
	workers, exist := m.Worker(topic)
	if !exist {
		return false
	}
	m.workerLock.Lock()
	delete(m.workers, topic)
	m.workerLock.Unlock()
	workers.Stop()
	return true
}

func (m *manage) Workers() []Worker {
	var works []Worker
	m.workerLock.Lock()
	for i := range m.workers {
		works = append(works, m.workers[i])
	}
	m.workerLock.Unlock()
	return works
}

func (m *manage) consumeWorker(work Worker) error {
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

func (m *manage) PublishWorker(work Worker) {
	m.workChan.In <- work
}

func (m *manage) StartRegisterServer(channel string) Worker {
	work, b := m.Worker(m.config.RegisterName)
	if b {
		return work
	}
	work = NewConsumeWork(m.config.RegisterName, channel)
	m.ConsumeWorker(work, 0)
	return work
}

func (m *manage) ConsumeWorker(work Worker, delay int) {
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
	for _, w := range m.Workers() {
		w.Stop()
	}
}

func (m *manage) Wait() {
	<-m.ctx.Done()
}

func (m *manage) RegisterClient(channel string, message WorkMessage) Worker {
	m.PublishWorker(NewPublishWork(m.config.RegisterName, message))

	work := NewConsumeWork(message.Topic, channel)
	m.ConsumeWorker(work, 5)
	return work
}

func (m *manage) produceWorker() error {
	producer, err := nsq.NewProducer(m.config.ProducerAddr, m.nsqConfig)
	if err != nil {
		return err
	}
	defer producer.Stop()
	var work Worker
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
		workers:   make(map[string]Worker, 1),
		workChan:  NewWorkChan(5),
	}
}

func NewManager(ctx context.Context, config Config) Manager {
	return initManage(ctx, config)
}

type Manager interface {
	NsqConfig() *nsq.Config
	SetNSQConfig(nsqConfig *nsq.Config)
	RegistryWorker(work Worker) (Worker, bool)
	Worker(topic string) (Worker, bool)
	DestroyWorker(topic string) bool
	Workers() []Worker
	PublishWorker(work Worker)
	StartRegisterServer(channel string) Worker
	RegisterClient(channel string, message WorkMessage) Worker
	ConsumeWorker(work Worker, delay int)
	Start()
	Stop()
	Wait()
}
