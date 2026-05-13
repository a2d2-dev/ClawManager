SET @instance_runtime_type_column_exists = (
  SELECT COUNT(*)
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'instances'
    AND COLUMN_NAME = 'runtime_type'
);
SET @instance_runtime_type_column_sql = IF(
  @instance_runtime_type_column_exists = 0,
  'ALTER TABLE instances ADD COLUMN runtime_type ENUM(''desktop'', ''shell'') NOT NULL DEFAULT ''desktop'' AFTER type',
  'SELECT 1'
);
PREPARE instance_runtime_type_column_stmt FROM @instance_runtime_type_column_sql;
EXECUTE instance_runtime_type_column_stmt;
DEALLOCATE PREPARE instance_runtime_type_column_stmt;
