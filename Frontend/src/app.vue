<template>
  <div class="container mx-auto px-4 py-8">
    <h1 class="text-3xl font-bold mb-4">Email Search</h1>
    
    <!-- Stats -->
    <div class="mb-4 text-gray-600">
      Total Emails: {{ stats.totalEmails }}
    </div>

    <!-- Search Box -->
    <div class="mb-6">
      <input 
        type="text" 
        v-model="searchQuery"
        @keyup.enter="search"
        placeholder="Search emails..."
        class="w-full p-3 rounded-lg border border-gray-300"
      >
    </div>

    <!-- Loading State -->
    <div v-if="loading" class="text-center py-4">
      Loading...
    </div>

    <!-- Email Detail View -->
    <div v-if="selectedEmail" class="mb-6">
      <button 
        @click="selectedEmail = null" 
        class="mb-4 px-3 py-1 bg-gray-200 rounded hover:bg-gray-300"
      >
        &larr; Back to results
      </button>
      
      <div class="bg-white p-6 rounded-lg shadow-lg">
        <h2 class="text-xl font-bold mb-2">{{ selectedEmail._source.subject || 'No Subject' }}</h2>
        
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4 text-sm">
          <div>
            <span class="font-medium">From:</span> {{ selectedEmail._source.from || 'Unknown' }}
          </div>
          <div>
            <span class="font-medium">To:</span> {{ selectedEmail._source.to || 'Unknown' }}
          </div>
          <div>
            <span class="font-medium">Date:</span> {{ selectedEmail._source.date || 'Unknown' }}
          </div>
        </div>
        
        <div class="border-t pt-4 mt-2 whitespace-pre-wrap">
          {{ selectedEmail._source.content || 'No content available' }}
        </div>
      </div>
    </div>

    <!-- Results List (only shown when no email is selected) -->
    <div v-if="!selectedEmail" class="space-y-4">
      <div 
        v-for="(result, index) in results" 
        :key="index"
        @click="viewEmail(result)"
        class="bg-white p-4 rounded-lg shadow cursor-pointer hover:bg-gray-50"
      >
        <div class="font-medium">{{ result._source.subject || 'No Subject' }}</div>
        <div class="text-sm text-gray-600">From: {{ result._source.from || 'Unknown' }}</div>
        <div class="text-sm text-gray-600">To: {{ result._source.to || 'Unknown' }}</div>
        <div class="text-sm text-gray-500 mt-2">
          {{ result._source.content?.substring(0, 150) + '...' || 'No content available' }}
        </div>
      </div>
    </div>

    <!-- No Results -->
    <div v-if="!loading && searchQuery && !results.length && !selectedEmail" class="text-center py-4">
      No results found
    </div>
  </div>
</template>

<script>
export default {
  data() {
    return {
      searchQuery: '',
      results: [],
      loading: false,
      stats: {
        totalEmails: 0
      },
      selectedEmail: null
    }
  },
  methods: {
    async search() {
      if (!this.searchQuery.trim()) return;

      this.loading = true;
      this.selectedEmail = null; // Reset selected email when performing new search
      
      try {
        const response = await fetch(`/api/search?q=${encodeURIComponent(this.searchQuery)}`);
        if (!response.ok) {
          throw new Error(`API error: ${response.status}`);
        }
        
        const data = await response.json();
        console.log("API Response:", data);

        if (!data.hits || !data.hits.hits) {
          console.error("Unexpected API response format", data);
          this.results = [];
        } else {
          this.results = [...data.hits.hits];
        }
      } catch (error) {
        console.error("Search error:", error);
        this.results = [];
      } finally {
        this.loading = false;
      }
    },
    
    async viewEmail(email) {
      this.loading = true;
      try {
        // Fetch full email details using the ID
        const response = await fetch(`/api/email/${email._id}`);
        if (!response.ok) {
          console.error(`Error fetching email: ${response.status}`);
          // Fall back to using the search result if the fetch fails
          this.selectedEmail = email;
          return;
        }
        
        const data = await response.json();
        console.log("Email detail response:", data);
        this.selectedEmail = data;
      } catch (error) {
        console.error("Error viewing email:", error);
        // Fall back to using the search result if there's an error
        this.selectedEmail = email;
      } finally {
        this.loading = false;
      }
    },
    
    async getStats() {
      try {
        const response = await fetch('/api/stats');
        if (!response.ok) {
          throw new Error(`API error: ${response.status}`);
        }
        
        const data = await response.json();
        this.stats.totalEmails = data.total_emails;
      } catch (error) {
        console.error('Stats error:', error);
      }
    }
  },
  mounted() {
    this.getStats();
  }
}
</script>