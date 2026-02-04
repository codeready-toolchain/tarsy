// TARSy PoC - Frontend Application

class TARSyApp {
    constructor() {
        this.ws = null;
        this.sessions = new Map();
        this.currentSessionId = null;
        this.reconnectAttempts = 0;
        this.maxReconnectAttempts = 10;
        
        this.init();
    }

    init() {
        this.setupWebSocket();
        this.setupEventListeners();
        this.loadSessions();
    }

    setupWebSocket() {
        const wsUrl = `ws://${window.location.hostname}:8080/ws`;
        console.log('Connecting to WebSocket:', wsUrl);
        
        this.ws = new WebSocket(wsUrl);
        
        this.ws.onopen = () => {
            console.log('WebSocket connected');
            this.updateWSStatus(true);
            this.reconnectAttempts = 0;
        };
        
        this.ws.onclose = () => {
            console.log('WebSocket disconnected');
            this.updateWSStatus(false);
            this.attemptReconnect();
        };
        
        this.ws.onerror = (error) => {
            console.error('WebSocket error:', error);
        };
        
        this.ws.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data);
                this.handleWSMessage(message);
            } catch (e) {
                console.error('Failed to parse WebSocket message:', e);
            }
        };

        // Keepalive ping every 30 seconds
        setInterval(() => {
            if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                this.ws.send(JSON.stringify({ type: 'ping' }));
            }
        }, 30000);
    }

    attemptReconnect() {
        if (this.reconnectAttempts >= this.maxReconnectAttempts) {
            console.log('Max reconnect attempts reached');
            return;
        }
        
        this.reconnectAttempts++;
        const delay = Math.min(1000 * Math.pow(2, this.reconnectAttempts), 30000);
        
        console.log(`Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts})`);
        setTimeout(() => this.setupWebSocket(), delay);
    }

    updateWSStatus(connected) {
        const statusEl = document.getElementById('ws-status');
        statusEl.textContent = connected ? 'Connected' : 'Disconnected';
        statusEl.className = connected ? 'status connected' : 'status disconnected';
    }

    handleWSMessage(message) {
        console.log('WebSocket message:', message);
        
        switch (message.type) {
            case 'connected':
                console.log('WebSocket connection established');
                break;
                
            case 'pong':
                // Keepalive response
                break;
                
            case 'session.created':
                this.handleSessionCreated(message);
                break;
                
            case 'session.status':
                this.handleSessionStatus(message);
                break;
                
            case 'llm.thinking':
                this.handleThinking(message);
                break;
                
            case 'llm.response':
                this.handleResponse(message);
                break;
                
            case 'session.completed':
                this.handleSessionCompleted(message);
                break;
                
            case 'session.error':
                this.handleSessionError(message);
                break;
                
            case 'session.cancelled':
                this.handleSessionCancelled(message);
                break;
                
            case 'session.timeout':
                this.handleSessionTimeout(message);
                break;
                
            default:
                console.log('Unknown message type:', message.type);
        }
    }

    handleSessionCreated(message) {
        const session = message.data;
        this.sessions.set(session.id, session);
        this.renderSessionsList();
        this.selectSession(session.id);
    }

    handleSessionStatus(message) {
        const session = this.sessions.get(message.session_id);
        if (session) {
            session.status = message.data.status;
            this.renderSessionsList();
            this.updateCancelButton();
        }
    }

    handleThinking(message) {
        this.addOrUpdateMessage(message.session_id, 'thinking', message.data.content);
    }

    handleResponse(message) {
        this.addOrUpdateMessage(message.session_id, 'assistant', message.data.content);
    }

    handleSessionCompleted(message) {
        const session = this.sessions.get(message.session_id);
        if (session) {
            session.status = 'completed';
            session.messages = message.data.messages;
            this.renderSessionsList();
            this.renderMessages();
            this.updateCancelButton();
        }
    }

    handleSessionError(message) {
        const session = this.sessions.get(message.session_id);
        if (session) {
            session.status = 'failed';
            session.error = message.data.error;
            this.renderSessionsList();
            this.addMessage(message.session_id, 'system', `Error: ${message.data.error}`);
            this.updateCancelButton();
        }
    }

    handleSessionCancelled(message) {
        const session = this.sessions.get(message.session_id);
        if (session) {
            session.status = 'cancelled';
            this.renderSessionsList();
            this.addMessage(message.session_id, 'system', '❌ Processing was cancelled by user');
            this.updateCancelButton();
        }
    }

    handleSessionTimeout(message) {
        const session = this.sessions.get(message.session_id);
        if (session) {
            session.status = 'timed_out';
            this.renderSessionsList();
            const timeoutMsg = message.data.message || 'Processing timed out';
            this.addMessage(message.session_id, 'system', `⏱️ ${timeoutMsg}`);
            this.updateCancelButton();
        }
    }

    addOrUpdateMessage(sessionId, role, content) {
        const session = this.sessions.get(sessionId);
        if (!session) return;
        
        // Check if last message is the same role - update it
        const lastMsg = session.messages[session.messages.length - 1];
        if (lastMsg && lastMsg.role === role) {
            lastMsg.content = content;
        } else {
            session.messages.push({ role, content });
        }
        
        if (sessionId === this.currentSessionId) {
            this.renderMessages();
        }
    }

    addMessage(sessionId, role, content) {
        const session = this.sessions.get(sessionId);
        if (!session) return;
        
        session.messages.push({ role, content });
        
        if (sessionId === this.currentSessionId) {
            this.renderMessages();
        }
    }

    setupEventListeners() {
        const form = document.getElementById('alert-form');
        const input = document.getElementById('message-input');
        const cancelBtn = document.getElementById('cancel-btn');
        
        form.addEventListener('submit', async (e) => {
            e.preventDefault();
            
            const message = input.value.trim();
            if (!message) return;
            
            input.value = '';
            input.disabled = true;
            
            try {
                const response = await fetch('/api/alerts', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json'
                    },
                    body: JSON.stringify({ message })
                });
                
                if (!response.ok) {
                    throw new Error('Failed to submit alert');
                }
                
                const session = await response.json();
                console.log('Created session:', session);
                
            } catch (error) {
                console.error('Error submitting alert:', error);
                alert('Failed to submit alert: ' + error.message);
            } finally {
                input.disabled = false;
                input.focus();
            }
        });

        cancelBtn.addEventListener('click', () => {
            this.cancelCurrentSession();
        });
    }

    async loadSessions() {
        try {
            const response = await fetch('/api/sessions');
            const sessions = await response.json();
            
            sessions.forEach(session => {
                this.sessions.set(session.id, session);
            });
            
            this.renderSessionsList();
            
            if (sessions.length > 0) {
                this.selectSession(sessions[0].id);
            } else {
                this.showEmptyState();
            }
        } catch (error) {
            console.error('Error loading sessions:', error);
        }
    }

    renderSessionsList() {
        const container = document.getElementById('sessions-list');
        
        if (this.sessions.size === 0) {
            container.innerHTML = '<div class="empty-state">No sessions yet</div>';
            return;
        }
        
        const sessionsArray = Array.from(this.sessions.values())
            .sort((a, b) => new Date(b.created_at) - new Date(a.created_at));
        
        container.innerHTML = sessionsArray.map(session => `
            <div class="session-item ${session.id === this.currentSessionId ? 'active' : ''}" 
                 data-session-id="${session.id}"
                 onclick="app.selectSession('${session.id}')">
                <div class="session-id">${session.id.substring(0, 8)}</div>
                <span class="session-status ${session.status}">${session.status}</span>
            </div>
        `).join('');
    }

    selectSession(sessionId) {
        this.currentSessionId = sessionId;
        this.renderSessionsList();
        this.renderMessages();
        this.updateCancelButton();
    }

    async cancelCurrentSession() {
        if (!this.currentSessionId) return;
        
        const session = this.sessions.get(this.currentSessionId);
        if (!session || session.status !== 'processing') {
            return;
        }

        try {
            const response = await fetch(`/api/sessions/${this.currentSessionId}/cancel`, {
                method: 'POST'
            });

            if (!response.ok) {
                throw new Error('Failed to cancel session');
            }

            console.log('Cancelled session:', this.currentSessionId);
        } catch (error) {
            console.error('Error cancelling session:', error);
            alert('Failed to cancel session: ' + error.message);
        }
    }

    updateCancelButton() {
        const cancelBtn = document.getElementById('cancel-btn');
        const session = this.sessions.get(this.currentSessionId);
        
        // Only show cancel button for processing sessions
        if (session && session.status === 'processing') {
            cancelBtn.style.display = 'block';
        } else {
            cancelBtn.style.display = 'none';
        }
    }

    renderMessages() {
        const container = document.getElementById('messages');
        const session = this.sessions.get(this.currentSessionId);
        
        if (!session) {
            this.showEmptyState();
            return;
        }
        
        container.innerHTML = session.messages.map(msg => {
            const roleLabel = msg.role.charAt(0).toUpperCase() + msg.role.slice(1);
            return `
                <div class="message ${msg.role}">
                    <div class="message-role">${roleLabel}</div>
                    <div class="message-content">${this.escapeHtml(msg.content)}</div>
                </div>
            `;
        }).join('');
        
        // Scroll to bottom
        container.scrollTop = container.scrollHeight;
    }

    showEmptyState() {
        const container = document.getElementById('messages');
        container.innerHTML = `
            <div class="empty-state">
                <h3>Welcome to TARSy PoC</h3>
                <p>Enter a message below to start a new session</p>
            </div>
        `;
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

// Initialize app when DOM is ready
let app;
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => {
        app = new TARSyApp();
    });
} else {
    app = new TARSyApp();
}
