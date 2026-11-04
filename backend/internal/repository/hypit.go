package repository

import (
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm/clause"
)

// CreateHypitTask 写入一条复刻任务。
func (r *Repository) CreateHypitTask(task *model.HypitTask) error {
	return r.db.Create(task).Error
}

// HypitTask 按 ID 读取任务；带 userID 时限制归属，避免越权读取。
func (r *Repository) HypitTask(id string, userID string) (*model.HypitTask, error) {
	var task model.HypitTask
	query := r.db.Where("id = ?", id)
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

// HypitTasks 按用户分页倒序返回任务。
func (r *Repository) HypitTasks(userID string, limit int, offset int) ([]model.HypitTask, int64, error) {
	var items []model.HypitTask
	var total int64
	query := r.db.Model(&model.HypitTask{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("created_at desc").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// SaveHypitTask 全量保存任务。调用方负责在业务上做状态机校验。
func (r *Repository) SaveHypitTask(task *model.HypitTask) error {
	return r.db.Save(task).Error
}

// TouchHypitWorker 记录执行节点心跳。
func (r *Repository) TouchHypitWorker(worker *model.HypitWorker) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"host", "version", "last_seen_at", "updated_at"}),
	}).Create(worker).Error
}

// HypitWorkerSeenSince 判断是否存在最近心跳过的执行节点。
func (r *Repository) HypitWorkerSeenSince(since time.Time) (bool, error) {
	var count int64
	if err := r.db.Model(&model.HypitWorker{}).Where("last_seen_at >= ?", since).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ClaimHypitTask 以「最早排队的任务」为序领取一条任务。
// 先用原子 UPDATE 抢占，避免多节点同时领到同一条。
func (r *Repository) ClaimHypitTask(workerID string) (*model.HypitTask, error) {
	var candidate model.HypitTask
	if err := r.db.Where("status = ?", model.HypitTaskQueued).Order("created_at asc").First(&candidate).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	result := r.db.Model(&model.HypitTask{}).
		Where("id = ? AND status = ?", candidate.ID, model.HypitTaskQueued).
		Updates(map[string]any{
			"status":     model.HypitTaskRunning,
			"worker_id":  workerID,
			"started_at": now,
			"stage":      "已领取，准备执行",
			"progress":   1,
			"updated_at": now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return r.HypitTask(candidate.ID, "")
}
