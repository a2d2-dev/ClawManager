SET @system_image_runtime_type_column_exists = (
  SELECT COUNT(*)
  FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'system_image_settings'
    AND COLUMN_NAME = 'runtime_type'
);
SET @system_image_runtime_type_column_sql = IF(
  @system_image_runtime_type_column_exists = 0,
  'ALTER TABLE system_image_settings ADD COLUMN runtime_type ENUM(''desktop'', ''shell'') NOT NULL DEFAULT ''desktop'' AFTER instance_type',
  'SELECT 1'
);
PREPARE system_image_runtime_type_column_stmt FROM @system_image_runtime_type_column_sql;
EXECUTE system_image_runtime_type_column_stmt;
DEALLOCATE PREPARE system_image_runtime_type_column_stmt;
