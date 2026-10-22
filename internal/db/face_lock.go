package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FaceBackfillLockKey 是 cmd/facebackfill 单实例锁的 advisory lock key。
// 0x79787166 = "yxqf"，固定值，避免与其它 advisory lock 混用。
const FaceBackfillLockKey int64 = 0x79787166

// FaceBackfillLock 持有独占的数据库连接与 session 级 advisory lock。
// PostgreSQL 的 advisory lock 绑定会话，必须固定同一条连接，不能从连接池
// 随取随还，否则 unlock 可能落到另一条连接上。
type FaceBackfillLock struct {
	conn *pgxpool.Conn
}

// TryFaceBackfillLock 尝试获取 facebackfill 的单实例锁。拿不到锁时返回
// (nil, false, nil)，调用方应直接退出而不是重复处理照片。
func (d *DB) TryFaceBackfillLock(ctx context.Context) (*FaceBackfillLock, bool, error) {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("db: acquire face backfill lock conn: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, FaceBackfillLockKey).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("db: try face backfill lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}
	return &FaceBackfillLock{conn: conn}, true, nil
}

// Release 释放 advisory lock 并归还连接；可安全重复调用。释放用的 ctx
// 应独立于可能已取消的主流程，避免进程退出时连接未归还。
func (l *FaceBackfillLock) Release(ctx context.Context) {
	if l == nil || l.conn == nil {
		return
	}
	_, _ = l.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, FaceBackfillLockKey)
	l.conn.Release()
	l.conn = nil
}
