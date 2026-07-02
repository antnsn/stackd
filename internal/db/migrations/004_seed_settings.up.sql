INSERT INTO settings (key, value, sensitive) VALUES
    ('dashboard_token',       '',   1),
    ('default_sync_interval', '60', 0)
ON CONFLICT (key) DO NOTHING;
