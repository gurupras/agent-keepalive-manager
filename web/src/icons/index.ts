import { addCollection } from '@iconify/vue/offline'
import collections from 'virtual:icon-collections'

// Register the build-time icon subset with the offline Iconify build, which
// has no icon API client at all: unknown names render nothing.
export function registerIcons () {
  for (const c of collections) addCollection(c)
}
