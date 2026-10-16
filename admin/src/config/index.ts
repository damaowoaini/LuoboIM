// Admin deployment uses the same-origin reverse proxy by default. This file
// contains public endpoint configuration only; no service secret is shipped.
const origin = typeof window !== 'undefined' ? window.location.origin : '';
const wsOrigin = origin.replace(/^http/, 'ws');
export const WS_URL = `${wsOrigin}/msg_gateway`;
export const API_URL = `${origin}/api`;
export const CHAT_URL = `${origin}/chat`;
export const ACCOUNT_URL = `${origin}/complete_admin`;
export const OBJECT_STORAGE = 'minio';
