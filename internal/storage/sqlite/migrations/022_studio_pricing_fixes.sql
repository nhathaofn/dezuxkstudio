-- Phase 3.1: Align Veo video pricing catalog seed rows with canonical Gemini provider IDs.

INSERT OR IGNORE INTO studio_pricing_catalog (id, provider_id, model_id, capability, unit, unit_price_usd, currency, updated_at) VALUES
    ('price_gemini_aistudio_veo', 'gemini-aistudio', '*', 'video', 'second', 0.150, 'USD', 1763337600000),
    ('price_gemini_vertex_veo',   'gemini-vertex',   '*', 'video', 'second', 0.150, 'USD', 1763337600000);
