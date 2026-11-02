package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tcpconnbenchmark/internal/tester"
)

func runCLI(cfg tester.Config, save bool) error {
	engine, err := tester.New(cfg)
	if err != nil {
		return fmt.Errorf("启动测试失败: %w", err)
	}
	defer func() {
		if err := engine.Discard(); err != nil {
			fmt.Fprintf(os.Stderr, "清理测试暂存失败: %v\n", err)
		}
	}()

	snap := engine.Snapshot()
	fmt.Println("tcpconnbenchmark · TCP 并发连接测试（CLI）")
	fmt.Printf("模式: %s　出口网卡: %s　域名: %d 个\n", snap.ModeLabel, snap.InterfaceLabel, snap.DomainCount)
	fmt.Printf("时长: %d 秒　失败停止: %s　发起速率: %d 次/秒\n", snap.DurationSec, failStopLabel(snap.FailStop), snap.RatePerSec)
	fmt.Println("按 Ctrl+C 可提前停止；正在建立连接…")

	engine.Start()
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopCh)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stopCh:
			fmt.Println("\n正在停止测试…")
			engine.Stop("手动停止")
		case <-done:
		}
	}()

	lastPrint := time.Time{}
	for {
		snap = engine.Snapshot()
		if snap.Finished {
			break
		}
		if time.Since(lastPrint) >= time.Second {
			fmt.Printf("[%5.1fs/%3ds] 尝试 %6d　成功 %6d　失败 %5d　进行中 %5d　拨号 %3d\n",
				snap.ElapsedSec, snap.DurationSec, snap.Attempts, snap.Success, snap.Failed, snap.Active, snap.Dialing)
			lastPrint = time.Now()
		}
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Println()
	fmt.Println("测试结束")
	fmt.Printf("停止原因: %s\n", snap.StopReason)
	successRate := "—"
	if snap.Success+snap.Failed > 0 {
		successRate = fmt.Sprintf("%.2f%%", snap.SuccessRate)
	}
	fmt.Printf("尝试 %d　成功 %d　失败 %d　中止 %d　成功率 %s\n",
		snap.Attempts, snap.Success, snap.Failed, snap.Aborted, successRate)
	fmt.Printf("峰值并发: %d　平均发起: %.1f 次/秒\n", snap.Max, snap.AvgRate)
	if snap.LogError != "" {
		fmt.Fprintf(os.Stderr, "日志错误: %s\n", snap.LogError)
	}
	if !save {
		fmt.Println("日志未导出；可使用 -save 显式保存。")
		return nil
	}
	dir, err := tester.LogDirectory()
	if err != nil {
		return fmt.Errorf("获取日志目录失败: %w", err)
	}
	exported, err := engine.Export(dir)
	if err != nil {
		return fmt.Errorf("导出日志失败: %w", err)
	}
	fmt.Printf("日志: %s\n摘要: %s\n", exported.LogPath, exported.SummaryPath)
	return nil
}

func failStopLabel(n int) string {
	if n <= 0 {
		return "不限制"
	}
	return fmt.Sprintf("%d 次", n)
}
