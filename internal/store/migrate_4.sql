-- Netflix ident skip (the owner, 2026-10-23): seconds the Play button skips at the start of a
-- Netflix-sourced file (name tagged NF / Netflix / NFLX). NULL = use the global setting
-- open.intro_skip_sec; 0 = never skip for this show; N = skip N seconds.
ALTER TABLE shows ADD COLUMN intro_skip_sec INTEGER;
