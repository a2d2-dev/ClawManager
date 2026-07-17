SET @stmt = IF(
  (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'instances'
      AND COLUMN_NAME = 'instance_mode'
      AND COLUMN_TYPE = 'enum(''lite'',''isolated'',''pro'')'
  ) = 0,
  'ALTER TABLE instances MODIFY COLUMN instance_mode ENUM(''lite'', ''isolated'', ''pro'') NOT NULL DEFAULT ''lite''',
  'SELECT 1'
);
PREPARE stmt FROM @stmt;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
