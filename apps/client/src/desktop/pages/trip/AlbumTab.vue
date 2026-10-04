<script setup lang="ts">
import { ElAlert, ElButton, ElEmpty, ElSkeleton } from 'element-plus'
import { computed, onMounted, ref } from 'vue'
import AssetFile from '@/desktop/components/AssetFile.vue'
import IconAction from '@/desktop/components/IconAction.vue'
import PhotoDialog from '@/desktop/components/PhotoDialog.vue'
import { listPhotos, deletePhoto, type Photo } from '@/shared/api/photos'
import { useTripContext } from '@/shared/travel/tripContext'
import { useContentCollection } from '@/shared/travel/useContentCollection'
const context = useTripContext()
const dialog = ref<InstanceType<typeof PhotoDialog>>()
const collection = useContentCollection(
  (cursor) => listPhotos(context.tripId, { limit: 24, cursor }),
  (item: Photo, key) => deletePhoto(context.tripId, item.id, item.version, key),
)
const { items, loading, error, notice, cursor, deleting } = collection
const groups = computed(() => {
  const days = new Map<string, Photo[]>()
  for (const item of items.value)
    days.set(item.recorded_on, [...(days.get(item.recorded_on) ?? []), item])
  return Array.from(days, ([day, photos]) => ({ day, photos }))
})
onMounted(() => void collection.reload())
</script>
<template>
  <section class="album-tab">
    <div class="album-toolbar tf-actions">
      <h2>相册</h2>
      <ElButton type="primary" @click="dialog?.open()">添加照片</ElButton>
      <IconAction icon="refresh" label="刷新相册" :loading="loading" @click="collection.reload()" />
    </div>
    <ElAlert v-if="error" :title="error" type="error" :closable="false" />
    <ElAlert v-if="notice" :title="notice" type="success" @close="notice = ''" />
    <ElSkeleton v-if="loading && !items.length" :rows="5" animated />
    <ElEmpty v-else-if="!items.length && !error" description="还没有照片，添加照片记录旅途。" />
    <section v-for="group in groups" :key="group.day" class="album-day" :aria-label="group.day">
      <h3>{{ group.day }}</h3>
      <div class="album-grid">
        <article v-for="photo in group.photos" :key="photo.id" class="photo-card tf-surface">
          <AssetFile :asset-id="photo.asset_id" :trip-id="context.tripId" />
          <p v-if="photo.caption" class="photo-caption">{{ photo.caption }}</p>
          <p class="photo-meta">
            {{ photo.taken_at_local?.replace('T', ' ') ?? '未填写拍摄时刻' }}
          </p>
          <p v-if="photo.place_name || photo.address" class="photo-meta">
            {{ [photo.place_name, photo.address].filter(Boolean).join(' · ') }}
          </p>
          <p v-if="photo.latitude != null" class="photo-meta">
            坐标 {{ photo.latitude }}, {{ photo.longitude }}
          </p>
          <div class="tf-actions">
            <IconAction
              icon="edit"
              :label="'编辑照片' + (photo.caption || '')"
              @click="dialog?.open(photo)"
            />
            <IconAction
              icon="trash"
              :label="'删除照片' + (photo.caption || '')"
              :loading="deleting === photo.id"
              @click="collection.destroy(photo, '删除这张照片记录？其他内容中的文件引用将保留。')"
            />
          </div>
        </article>
      </div>
    </section>
    <ElButton v-if="cursor" :loading="loading" @click="collection.reload(true)"
      >加载更多照片</ElButton
    >
    <PhotoDialog ref="dialog" @saved="collection.saved" />
  </section>
</template>
<style scoped>
.album-tab {
  display: grid;
  gap: 20px;
}
.album-toolbar h2 {
  margin: 0 auto 0 0;
  font-size: 20px;
}
.album-day h3 {
  margin: 0 0 12px;
}
.album-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.photo-card {
  padding: 20px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-card);
  background: var(--tf-surface);
  min-width: 0;
}
.photo-caption {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.photo-meta {
  color: var(--tf-text-2);
  font-size: 13px;
  overflow-wrap: anywhere;
}
@media (max-width: 800px) {
  .album-grid {
    grid-template-columns: 1fr;
  }
  .photo-card {
    padding: 16px;
  }
}
</style>
