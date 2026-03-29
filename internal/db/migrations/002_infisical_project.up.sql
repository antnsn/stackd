INSERT INTO settings (key, value, sensitive) VALUES
    ('infisical_project_id', '', 0)
ON CONFLICT (key) DO NOTHING;
