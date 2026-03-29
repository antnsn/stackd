INSERT INTO settings (key, value, sensitive) VALUES ('pull_only', 'false', 0) ON CONFLICT (key) DO NOTHING;
