CREATE TABLE IF NOT EXISTS system_image_settings (
  id INT AUTO_INCREMENT PRIMARY KEY,
  instance_type VARCHAR(50) NOT NULL,
  runtime_type ENUM('desktop', 'shell') NOT NULL DEFAULT 'desktop',
  display_name VARCHAR(255) NOT NULL,
  image VARCHAR(500) NOT NULL,
  is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_instance_type (instance_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
