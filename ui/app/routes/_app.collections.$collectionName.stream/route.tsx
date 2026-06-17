// Resource route: collection streaming (CDC) toggle.
//
// No default export -> data-only route, addressed at
// /collections/:collectionName/stream (PUT / DELETE). Driven from the
// Settings page via useFetcher.submit.
export { clientAction } from "./action.client";