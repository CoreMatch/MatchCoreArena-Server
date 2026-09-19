package service

import (
	"context"
	"database/sql"
	"fmt"
)

// MaintenanceService 提供维护相关的业务逻辑，如执行原始 SQL。
type MaintenanceService interface {
	ExecuteSQL(ctx context.Context, query string) (interface{}, error)
}

type maintenanceService struct {
	db *sql.DB
}

// NewMaintenanceService 创建 MaintenanceService 实例。
func NewMaintenanceService(db *sql.DB) MaintenanceService {
	return &maintenanceService{db: db}
}

// ExecuteSQL 执行原始 SQL 语句。
// 如果是 SELECT 语句，返回 []map[string]interface{}；
// 如果是 INSERT/UPDATE/DELETE 等，返回影响的行数或执行结果摘要。
func (s *maintenanceService) ExecuteSQL(ctx context.Context, query string) (interface{}, error) {
	// 为了简单起见，我们根据是否包含 SELECT (不区分大小写) 来决定使用 Query 还是 Exec。
	// 注意：这只是一个基础实现，实际中可能需要更复杂的 SQL 解析。
	
	// 尝试作为查询执行
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		// 如果 Query 失败，尝试作为非查询语句执行
		result, execErr := s.db.ExecContext(ctx, query)
		if execErr != nil {
			return nil, fmt.Errorf("执行 SQL 失败 (Query: %v, Exec: %v)", err, execErr)
		}
		
		rowsAffected, _ := result.RowsAffected()
		lastInsertID, _ := result.LastInsertId()
		return map[string]interface{}{
			"rows_affected": rowsAffected,
			"last_insert_id": lastInsertID,
			"message": "SQL 执行成功",
		}, nil
	}
	defer rows.Close()

	// 处理查询结果
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("获取列信息失败: %w", err)
	}

	var results []map[string]interface{}
	for rows.Next() {
		// 准备动态接收数据的切片
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("扫描行数据失败: %w", err)
		}

		// 将行数据转换为 map
		row := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			// 处理 MySQL 返回的 []byte 数据（通常是字符串）
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取结果集失败: %w", err)
	}

	return results, nil
}
