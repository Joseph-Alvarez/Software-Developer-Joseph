const { createApp } = Vue

//Inicializamos la App
const app = createApp({
    data() {
        return {
            searchQuery: '',
            results: [],
            loading: false,
            stats: {
                totalEmails: 0
            },
            selectedEmail: null,
            selectedEmailContent: {},
            emailContents: {}
        }
    },
    methods: {
        async search() {
            if (!this.searchQuery.trim()) return;
            
            this.loading = true;
            try {
                console.log('Searching for:', this.searchQuery);
                const response = await fetch(`/api/search?q=${encodeURIComponent(this.searchQuery)}`);
                console.log('Search response status:', response.status);
                const data = await response.json();
                console.log('Search results:', data);
                
                // Handle different API response structures
                if (data.hits && data.hits.hits) {
                    this.results = data.hits.hits;
                    this.stats.totalEmails = data.hits.total?.value || data.hits.total || 0;
                } else if (Array.isArray(data)) {
                    this.results = data.map((item, index) => ({ _id: item.id || index, _source: item }));
                    this.stats.totalEmails = data.length;
                } else {
                    this.results = [];
                    console.error('Unexpected API response format:', data);
                }
                
                this.selectedEmail = null;
                this.selectedEmailContent = {};
            } catch (error) {
                console.error('Search error:', error);
                this.results = [];
            } finally {
                this.loading = false;
            }
        },
        selectEmail(email) {
            this.selectedEmail = email;
            
            // Check if we already have the content cached
            if (this.emailContents[email._id]) {
                this.selectedEmailContent = this.emailContents[email._id];
            } else {
                this.fetchEmailContent(email._id);
            }
        },
        async fetchEmailContent(emailId) {
            try {
                const response = await fetch(`/api/email/${emailId}`);
                const data = await response.json();
                
                // Store content in cache
                if (data) {
                    // Handle different API response structures
                    if (data._source) {
                        this.emailContents[emailId] = data._source;
                        this.selectedEmailContent = data._source;
                    } else {
                        this.emailContents[emailId] = data;
                        this.selectedEmailContent = data;
                    }
                }
            } catch (error) {
                console.error('Error fetching email content:', error);
                // Fall back to the search result data if available
                if (this.selectedEmail && this.selectedEmail._source) {
                    this.selectedEmailContent = this.selectedEmail._source;
                }
            }
        },
        async fetchStats() {
            try {
                console.log('Fetching stats...');
                const response = await fetch('/api/stats');
                console.log('Stats response status:', response.status);
                const data = await response.json();
                console.log('Stats data:', data);
                this.stats = data || { totalEmails: 0 };
            } catch (error) {
                console.error('Stats error:', error);
            }
        },
        formatDate(dateString) {
            if (!dateString) return '';
            
            try {
                const date = new Date(dateString);
                return date.toLocaleString();
            } catch {
                return dateString;
            }
        },
        cleanEmailContent(content) {
            if (!content) return '';
            
            // More comprehensive pattern to remove all variations of email headers
            let cleanedContent = content;
            
            // Remove lines with "----- Original Message -----" or similar
            cleanedContent = cleanedContent.replace(/-----\s*Original Message\s*-----.*$/gm, '');
            
            // Remove standard email header lines
            cleanedContent = cleanedContent.replace(/^(From|To|Sent|Subject|Date|Cc):\s.*$/gm, '');
            
            // Remove quoted header parts with email addresses
            cleanedContent = cleanedContent.replace(/^From:.*?@.*$/gm, '');
            cleanedContent = cleanedContent.replace(/^To:.*$/gm, '');
            
            // Remove date/time header lines that appear in forwarded messages
            cleanedContent = cleanedContent.replace(/^Sent:.*?(AM|PM)$/gm, '');
            cleanedContent = cleanedContent.replace(/^Sent:.*(Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday).*$/gm, '');
            
            // Remove lines containing just ">"
            cleanedContent = cleanedContent.replace(/^>+\s*$/gm, '');
            
            // Remove lines starting with ">" followed by any content
            cleanedContent = cleanedContent.replace(/^>\s.*$/gm, '');
            
            // Try to remove entire forwarded message sections
            const forwardedPatterns = [
                /---+ *?Forwarded.*?---+[\s\S]*?From:[\s\S]*?Subject:.*$/gm,
                /---+ *?Original Message.*?---+[\s\S]*?From:[\s\S]*?Subject:.*$/gm
            ];
            
            forwardedPatterns.forEach(pattern => {
                cleanedContent = cleanedContent.replace(pattern, '');
            });
            
            // Add more specific patterns
            const customPatterns = [
                /.*Original Message.*$/gm,
                /From: ".*"/gm,
                /To: ".*"/gm,
                /^Sent: .*, [A-Za-z]+ \d+, \d+ \d+:\d+ (AM|PM)$/gm
            ];
            
            customPatterns.forEach(pattern => {
                cleanedContent = cleanedContent.replace(pattern, '');
            });
            
            // Remove extra line breaks that may be created
            cleanedContent = cleanedContent.replace(/\n{3,}/g, '\n\n');
            
            return cleanedContent.trim();
        },
        highlightSearchTerm(content) {
            if (!content || !this.searchQuery.trim()) return content;
            
            // Create a regular expression with the search query to match case-insensitive
            const regex = new RegExp(`(${this.escapeRegex(this.searchQuery)})`, 'gi');
            
            // Replace matches with highlighted span
            return content.replace(regex, '<span class="bg-yellow-200">$1</span>');
        },
        escapeRegex(string) {
            return string.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        },
        getEmailPreview(content) {
            if (!content) return 'No content available';
            
            // Clean the content first
            const cleanedContent = this.cleanEmailContent(content);
            
            // Return first 50 characters
            return cleanedContent.length > 50 
                ? cleanedContent.substring(0, 50) + '...' 
                : cleanedContent;
        }
    },
    mounted() {
        console.log('App mounted, fetching initial stats...');
        this.fetchStats();
    }
})

app.mount('#app')