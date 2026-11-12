ALTER TABLE reading_sessions ADD COLUMN intent TEXT NOT NULL DEFAULT 'start' CHECK(intent IN('start','restart'));
