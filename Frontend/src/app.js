const { createApp } = Vue

const app = createApp({
    data() {
        return {
            searchQuery: '',
            results: [],
            loading: false,
            stats: {
                totalEmails: 0
            }
        }
    },
    methods: {
        async search() {
            if (!this.searchQuery.trim()) return
            
            this.loading = true
            try {
                console.log('Searching for:', this.searchQuery)
                const response = await fetch(`/api/search?q=${encodeURIComponent(this.searchQuery)}`)
                console.log('Search response status:', response.status)
                const data = await response.json()
                console.log('Search results:', data)
                this.results = data.hits.hits
            } catch (error) {
                console.error('Search error:', error)
                this.results = []
            } finally {
                this.loading = false
            }
        },
        async getStats() {
            try {
                console.log('Fetching stats...')
                const response = await fetch('/api/stats')
                console.log('Stats response status:', response.status)
                const data = await response.json()
                console.log('Stats data:', data)
                this.stats.totalEmails = data.total_emails
            } catch (error) {
                console.error('Stats error:', error)
            }
        }
    },
    mounted() {
        console.log('App mounted, fetching initial stats...')
        this.getStats()
    }
})

app.mount('#app')