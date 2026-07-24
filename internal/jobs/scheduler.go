package jobs

import (
	"context"
	"log"
	"time"
)

// Job 描述一个定时任务。
type Job struct {
	Name     string
	Interval time.Duration
	Fn       func(ctx context.Context) error
	// InitialDelay 推迟首次执行。零值表示 Start 时立即执行一次。
	// 用于需要等系统进入稳态才有意义的任务（如配置对账要等节点完成重连）。
	InitialDelay time.Duration
}

// Scheduler 管理一组定时任务，每个任务在独立 goroutine 中运行。
// 任务间的数据一致性由 jobs.go 中的 mu 在各 job 函数内部保护，
// 不在调度层持锁，避免网络 IO 期间阻塞其他任务。
type Scheduler struct {
	jobs   []Job
	logger *log.Logger
}

// NewScheduler 创建调度器；logger 为 nil 时使用默认 log 包。
func NewScheduler(logger *log.Logger) *Scheduler {
	if logger == nil {
		logger = log.Default()
	}
	return &Scheduler{logger: logger}
}

// Add 注册一个任务，必须在 Start 前调用。
func (s *Scheduler) Add(job Job) {
	s.jobs = append(s.jobs, job)
}

// Start 启动所有任务，随 ctx 取消而停止。
// 每个任务启动时立即执行一次，之后按 Interval 周期重复。
func (s *Scheduler) Start(ctx context.Context) {
	for _, job := range s.jobs {
		go s.run(ctx, job)
	}
}

func (s *Scheduler) run(ctx context.Context, job Job) {
	if job.InitialDelay > 0 {
		timer := time.NewTimer(job.InitialDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	s.execute(ctx, job)

	ticker := time.NewTicker(job.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.execute(ctx, job)
		}
	}
}

func (s *Scheduler) execute(ctx context.Context, job Job) {
	if err := job.Fn(ctx); err != nil {
		s.logger.Printf("[jobs] %s error: %v", job.Name, err)
	}
}
