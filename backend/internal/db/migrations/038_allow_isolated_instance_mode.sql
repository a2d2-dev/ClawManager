ALTER TABLE instances
  MODIFY COLUMN instance_mode ENUM('lite', 'isolated', 'pro') NOT NULL DEFAULT 'lite';
