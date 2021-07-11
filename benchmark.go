package BigBFT

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/salemmohammed/BigBFT/log"
)

// DB is general interface implemented by client to call client library
type DB interface {
	Init() error
	Write(key int, value []byte,Globalcounter int) error
	Stop() error
}
// Bconfig holds all benchmark configuration
type Bconfig struct {
	T                    int     // total number of running time in seconds
	N                    int     // total number of requests
	K                    int     // key sapce
	W                    float64 // write ratio
	Throttle             int     // requests per second throttle, unused if 0
	Concurrency          int     // number of simulated clients
	Distribution         string  // distribution
	LinearizabilityCheck bool    // run linearizability checker at the end of benchmark

	// conflict distribution
	Conflicts int // percentage of conflicting keys
	Min       int // min key

	// normal distribution
	Mu    float64 // mu of normal distribution
	Sigma float64 // sigma of normal distribution
	Move  bool    // moving average (mu) of normal distribution
	Speed int     // moving speed in milliseconds intervals per key

	// zipfian distribution
	ZipfianS float64 // zipfian s parameter
	ZipfianV float64 // zipfian v parameter

	// exponential distribution
	Lambda float64 // rate parameter

	Size int // payload size
}
// DefaultBConfig returns a default benchmark config
func DefaultBConfig() Bconfig {
	return Bconfig{
		T:                    60,
		N:                    0,
		K:                    1000,
		W:                    1,
		Throttle:             0,
		Concurrency:          1,
		Distribution:         "uniform",
		LinearizabilityCheck: true,
		Conflicts:            100,
		Min:                  0,
		Mu:                   0,
		Sigma:                60,
		Move:                 false,
		Speed:                500,
		ZipfianS:             2,
		ZipfianV:             1,
		Lambda:               0.01,
		Size:				  128,
	}
}
// Benchmark is benchmarking tool that generates workload and collects operation history and latency
type Benchmark struct {
	db DB // read/write operation interface
	Bconfig
	//*History

	rate      *Limiter
	latency   []time.Duration // latency per operation
	startTime time.Time
	zipf      *rand.Zipf
	counter   int
	wait sync.WaitGroup // waiting for all generated keys to complete
	globalCouner int
}
// NewBenchmark returns new Benchmark object given implementation of DB interface
func NewBenchmark(db DB) *Benchmark {
	b := new(Benchmark)
	b.db = db
	b.counter = -1
	b.globalCouner = -1
	b.Bconfig = config.Benchmark
	//b.History = NewHistory()
	if b.Throttle > 0 {
		b.rate = NewLimiter(b.Throttle)
	}
	rand.Seed(time.Now().UTC().UnixNano())
	r := rand.New(rand.NewSource(time.Now().UTC().UnixNano()))
	b.zipf = rand.NewZipf(r, b.ZipfianS, b.ZipfianV, uint64(b.K))
	return b
}
// Load will create all K keys to DB
func (b *Benchmark) Load() {
	b.W = 1.0
	b.Throttle = 0

	b.db.Init()
	// Buffered Channels
	keys := make(chan int, b.Concurrency)
	latencies := make(chan time.Duration, 1000)
	globalCouner := make(chan int, 0)

	defer close(latencies)
	go b.collect(latencies)

	b.startTime = time.Now()
	for i := 0; i < b.Concurrency; i++ {
		go b.worker(keys, latencies,globalCouner)
	}
	for i := b.Min; i < b.Min+b.K; i++ {
		b.wait.Add(1)
		keys <- i
		//b.globalCouner++
		globalCouner <- b.globalCouner
	}
	t := time.Now().Sub(b.startTime)

	b.db.Stop()
	close(keys)
	b.wait.Wait()
	stat := Statistic(b.latency)

	log.Infof("Benchmark took %v\n", t)
	log.Infof("Throughput %f\n", float64(len(b.latency))/t.Seconds())
	log.Info(stat)
}
// Run starts the main logic of benchmarking
func (b *Benchmark) Run() {
	var stop chan bool
	if b.Move {
		log.Debugf("This move is false all the time")
		move := func() { b.Mu = float64(int(b.Mu+1) % b.K) }
		stop = Schedule(move, time.Duration(b.Speed)*time.Millisecond)
		defer close(stop)
	}

	b.latency = make([]time.Duration, 0)
	keys := make(chan int, b.Concurrency)
	latencies := make(chan time.Duration, 1000)

	globalCouner := make(chan int, 0)

	defer close(latencies)
	go b.collect(latencies)

	// number of threads or concurrency
	for i := 0; i < b.Concurrency; i++ {
		// this b is object calls worker function
		go func() {
			b.worker(keys,latencies,globalCouner)
		}()
	}
	b.db.Init()
	b.startTime = time.Now()
	if b.T > 0 {
		timer := time.NewTimer(time.Second * time.Duration(b.T))
	loop:
		for {
			select {
			case <-timer.C:
				break loop
			default:
				b.wait.Add(1)
				keys <- b.next()
				b.globalCouner++
				globalCouner <- b.globalCouner
			}
		}
	} else {
		for i := 0; i < b.N; i++ {
			b.wait.Add(1)
			keys <- b.next()
			b.globalCouner++
			globalCouner <- b.globalCouner
		}
		b.wait.Wait()
	}
	t := time.Now().Sub(b.startTime)

	b.db.Stop()
	close(keys)
	close(globalCouner)
	log.Debugf("--------------------done -------------2")
	stat := Statistic(b.latency)
	log.Infof("Concurrency = %d", b.Concurrency)
	log.Infof("Write Ratio = %f", b.W)
	log.Infof("Number of Keys = %d", b.K)
	log.Infof("Benchmark Time = %v\n", t)
	log.Infof("Throughput = %f\n", float64(len(b.latency))/t.Seconds())
	log.Info(stat)

	stat.WriteFile("latency")
	//b.History.WriteFile("history")
}
// generates key based on distribution
func (b *Benchmark) next() int {
	var key int
	switch b.Distribution {
	case "order":
		b.counter = (b.counter + 1) % b.K
		key = b.counter + b.Min

	case "uniform":
		key = rand.Intn(b.K) + b.Min

	case "conflict":
		if rand.Intn(100) < b.Conflicts {
			key = 0
		} else {
			b.counter = (b.counter + 1) % b.K
			key = b.counter + b.Min
		}

	case "normal":
		key = int(rand.NormFloat64()*b.Sigma + b.Mu)
		for key < 0 {
			key += b.K
		}
		for key > b.K {
			key -= b.K
		}

	case "zipfan":
		key = int(b.zipf.Uint64())

	case "exponential":
		key = int(rand.ExpFloat64() / b.Lambda)

	default:
		log.Fatalf("unknown distribution %s", b.Distribution)
	}

	if b.Throttle > 0 {
		b.rate.Wait()
	}

	return key
}
// this where client do the work from benchmark
func (b *Benchmark) worker(keys <-chan int, result chan<- time.Duration, globalCouner <- chan int) {
	var s time.Time
	var e time.Time
	var err error
	var v []byte
	//data := make([]byte, 4)
	for k := range keys {
		op := new(operation)
		if rand.Float64() < b.W {
			v = GenerateRandVal(b.Bconfig.Size)
			s = time.Now()
			err = b.db.Write(k, v,<- globalCouner)
			e = time.Now()
			op.input = v
		} else {
			s = time.Now()
			e = time.Now()
			op.output = v
		}
		op.start = s.Sub(b.startTime).Nanoseconds()
		if err == nil {
			op.end = e.Sub(b.startTime).Nanoseconds()
			result <- e.Sub(s)
		} else {
			op.end = math.MaxInt64
			log.Error(err)
		}
	}
}
func (b *Benchmark) collect(latencies <-chan time.Duration) {
	for t := range latencies {
		b.latency = append(b.latency, t)
		b.wait.Done()
	}
	log.Debugf("time = %v", b.latency)
}