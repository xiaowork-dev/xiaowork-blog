<script setup lang="ts">
useHead({ title: 'XiaoWork Blog' })
</script>

<template>
  <main class="workspace">
    <aside class="profile" aria-label="博客个人信息占位">
      <span class="avatar">X</span>
      <h2>XiaoWork</h2>
      <p>未来将在这里显示头像、个人简介和 GitHub 等图标。</p>
    </aside>
    <section class="content">
      <div class="kicker">开发中 · P2 账号系统</div>
      <h1>博客正在建设中</h1>
      <p>当前已提供注册、邮箱验证、登录和找回密码。文章、评论和后台功能将在后续阶段上线。</p>
      <div class="note">技术基线：Nuxt 4 · Go/Chi · PostgreSQL 18</div>
      <p class="disclaimer">后续会按既定 PRD 与视觉稿逐阶段实现，当前页面不代表业务功能已完成。</p>
    </section>
  </main>
</template>
