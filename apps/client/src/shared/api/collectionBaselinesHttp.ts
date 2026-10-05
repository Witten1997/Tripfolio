import { api } from './client'
import { ApiError } from './problem'
import {
  loadCollectionBaseline,
  type CollectionPageRequest,
  type CollectionScope,
  type CompleteCollection,
  type ReadableCollectionKind,
} from './collectionBaselines'

export async function readCollectionBaselinePage<K extends ReadableCollectionKind>(
  request: CollectionPageRequest<K>,
  signal?: AbortSignal,
): Promise<unknown> {
  const { data, error } = await api.GET('/collection-baselines', {
    params: { query: request },
    signal,
  })
  if (error || !data) throw new ApiError(error)
  return data
}

export function loadCollectionBaselineHttp<K extends ReadableCollectionKind>(
  scope: CollectionScope<K>,
  options?: { readonly limit?: number; readonly signal?: AbortSignal },
): Promise<CompleteCollection<K>> {
  return loadCollectionBaseline(scope, readCollectionBaselinePage, options)
}
