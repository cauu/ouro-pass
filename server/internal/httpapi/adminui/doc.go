// Package adminui embeds the built Ouro Pass Admin SPA (../../../../web) and
// serves it under /admin with history-API fallback. The dist is produced by
// `make web` (build ../web, stage here); a fresh checkout has only dist/.gitkeep,
// so the handler degrades to a placeholder until the SPA is built in.
package adminui
