/*
 * The server list page and the per-server page, in one chunk that loads the
 * first time somebody opens either — the console and the controls go with
 * them. The forum's every-page bundle keeps only the sidebar widget, the
 * store and the notification.
 */
export { default as ServersPage } from './components/ServersPage';
export { default as ServerPage } from './components/ServerPage';
