import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'agent-team Developer Docs',
  description: 'Developer documentation for agent-team, a file-backed CLI and daemon for orchestrating teams of LLM agents.',
  cleanUrls: true,
  lastUpdated: true,
  markdown: {
    lineNumbers: true
  },
  themeConfig: {
    logo: { text: 'agent-team' },
    search: {
      provider: 'local'
    },
    nav: [
      { text: 'Guide', link: '/guide/' },
      { text: 'Runtime', link: '/runtime/daemon' },
      { text: 'Workflows', link: '/workflows/jobs' },
      { text: 'Use Cases', link: '/use-cases/' },
      { text: 'Contributing', link: '/contributing/development' },
      { text: 'GitHub', link: 'https://github.com/jamesaud/agent-team' }
    ],
    sidebar: [
      {
        text: 'Guide',
        items: [
          { text: 'Overview', link: '/guide/' },
          { text: 'Concepts', link: '/guide/concepts' },
          { text: 'Architecture', link: '/guide/architecture' },
          { text: 'Repository Layout', link: '/guide/repository-layout' }
        ]
      },
      {
        text: 'Authoring',
        items: [
          { text: 'Templates', link: '/authoring/templates' },
          { text: 'Agents and Skills', link: '/authoring/agents-and-skills' },
          { text: 'Topology', link: '/authoring/topology' }
        ]
      },
      {
        text: 'Runtime',
        items: [
          { text: 'Daemon', link: '/runtime/daemon' },
          { text: 'Runtime Profiles', link: '/runtime/profiles' },
          { text: 'Instances', link: '/runtime/instances' },
          { text: 'Status, Mailbox, Channels', link: '/runtime/status-mailbox-channels' }
        ]
      },
      {
        text: 'Workflows',
        items: [
          { text: 'Jobs', link: '/workflows/jobs' },
          { text: 'Queues and Recovery', link: '/workflows/queues-and-recovery' },
          { text: 'Pipelines and Teams', link: '/workflows/pipelines-and-teams' },
          { text: 'Intake and Schedules', link: '/workflows/intake-and-schedules' },
          { text: 'Diagnostics and Repair', link: '/workflows/diagnostics-and-repair' }
        ]
      },
      {
        text: 'Reference',
        items: [
          { text: 'CLI Reference', link: '/reference/cli' },
          { text: 'File Formats', link: '/reference/file-formats' },
          { text: 'Runtime API', link: '/reference/runtime-api' }
        ]
      },
      {
        text: 'Use Cases',
        items: [
          { text: 'Use Case Index', link: '/use-cases/' },
          { text: 'Ticket to PR', link: '/use-cases/ticket-to-pr' },
          { text: 'Multi-Team Repo', link: '/use-cases/multi-team-repo' },
          { text: 'External Intake', link: '/use-cases/external-intake' },
          { text: 'Intake Deployment', link: '/use-cases/intake-deployment' },
          { text: 'On-call Recovery', link: '/use-cases/on-call-recovery' },
          { text: 'Template Authoring', link: '/use-cases/template-authoring' },
          { text: 'Topology Gallery', link: '/use-cases/topology-gallery' }
        ]
      },
      {
        text: 'Contributing',
        items: [
          { text: 'Development Workflow', link: '/contributing/development' },
          { text: 'Testing Strategy', link: '/contributing/testing' },
          { text: 'Roadmap Context', link: '/contributing/roadmap' }
        ]
      }
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/jamesaud/agent-team' }
    ],
    footer: {
      message: 'Pre-v1 developer documentation for agent-team.',
      copyright: 'Built with VitePress.'
    }
  }
})
