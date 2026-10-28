import { createRouter, createWebHashHistory } from 'vue-router'
import SessionsView from './views/SessionsView.vue'
import LedgerView from './views/LedgerView.vue'
import MissedView from './views/MissedView.vue'

// Hash history keeps every route on the same document URL, so the app works
// under any path prefix.
export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'sessions', component: SessionsView },
    { path: '/ledger', name: 'ledger', component: LedgerView },
    { path: '/missed', name: 'missed', component: MissedView },
    { path: '/:rest(.*)*', redirect: '/' },
  ],
})
