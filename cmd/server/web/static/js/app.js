// ONVIF Device Manager - Web Application

class ONVIFDeviceManager {
    constructor() {
        this.devices = [];
        this.currentDevice = null;
        this.peerConnection = null;
        this.selectedDiscoveredDevices = new Set();
        this.credentialResolve = null;
        this.credentialReject = null;
        this.currentUser = null;
        this.isAdmin = false;
        this.users = [];
        this.selectedUser = null;
        this.isNewUser = false;
        this.eventSource = null;
        this.activeEventSourceDevice = null;
        
        this.init();
    }

    async init() {
        this.initTheme();
        this.bindElements();
        this.bindEvents();
        this.loadVersion();
        await this.checkAuth();
    }

    async loadVersion() {
        try {
            const response = await fetch('/api/version');
            if (response.ok) {
                const data = await response.json();
                const el = document.getElementById('appVersion');
                if (el) el.textContent = `v${data.version}`;
            }
        } catch (e) {
            console.warn('Failed to load version', e);
        }
    }

    // Authentication
    async checkAuth() {
        try {
            // First check if setup is needed
            const setupResponse = await fetch('/api/auth/needs-setup');
            const setupData = await setupResponse.json();
            
            if (setupData.needsSetup) {
                this.showLoginOverlay(true);
                this.hideLoadingOverlay();
                return;
            }

            // Then check if user is logged in
            const response = await fetch('/api/auth/me');
            if (response.ok) {
                const data = await response.json();
                this.currentUser = data;
                this.isAdmin = data.isAdmin;
                this.hideLoginOverlay();
                this.updateAdminUI();
                this.loadDevices();
            } else {
                this.showLoginOverlay(false);
            }
        } catch (error) {
            console.error('Auth check failed:', error);
            this.showLoginOverlay(false);
        } finally {
            this.hideLoadingOverlay();
        }
    }

    hideLoadingOverlay() {
        const overlay = document.getElementById('loadingOverlay');
        if (overlay) {
            overlay.classList.add('hidden');
        }
    }

    showLoginOverlay(isFirstUser = false) {
        const overlay = document.getElementById('loginOverlay');
        const appContainer = document.getElementById('appContainer');
        const subtitle = document.getElementById('loginSubtitle');
        const loginBtn = document.getElementById('loginBtn');
        
        if (isFirstUser) {
            subtitle.textContent = 'Create the first admin account';
            loginBtn.textContent = 'Create Account';
        } else {
            subtitle.textContent = 'Please sign in to continue';
            loginBtn.textContent = 'Sign In';
        }
        
        overlay.classList.remove('hidden');
        appContainer.classList.add('hidden');
        this.isFirstUser = isFirstUser;
    }

    hideLoginOverlay() {
        const overlay = document.getElementById('loginOverlay');
        const appContainer = document.getElementById('appContainer');
        
        overlay.classList.add('hidden');
        appContainer.classList.remove('hidden');
    }

    updateAdminUI() {
        const adminElements = document.querySelectorAll('.admin-only');
        adminElements.forEach(el => {
            if (this.isAdmin) {
                el.classList.remove('hidden-for-user');
            } else {
                el.classList.add('hidden-for-user');
            }
        });
    }

    async handleLogin(e) {
        e.preventDefault();
        
        const username = document.getElementById('loginUsername').value;
        const password = document.getElementById('loginPassword').value;
        const loginError = document.getElementById('loginError');
        
        loginError.classList.add('hidden');
        
        try {
            const endpoint = this.isFirstUser ? '/api/auth/first-user' : '/api/auth/login';
            const response = await fetch(endpoint, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ username, password })
            });
            
            if (response.ok) {
                const data = await response.json();
                this.currentUser = data;
                this.isAdmin = data.isAdmin;
                this.hideLoginOverlay();
                this.updateAdminUI();
                this.loadDevices();
                this.showToast(this.isFirstUser ? 'Admin account created' : 'Logged in successfully', 'success');
            } else {
                const error = await response.json();
                loginError.textContent = error.message || 'Login failed';
                loginError.classList.remove('hidden');
            }
        } catch (error) {
            loginError.textContent = 'Connection error';
            loginError.classList.remove('hidden');
        }
    }

    async handleLogout() {
        try {
            await fetch('/api/auth/logout', { method: 'POST' });
        } catch (error) {
            console.error('Logout error:', error);
        }
        
        this.currentUser = null;
        this.isAdmin = false;
        this.devices = [];
        this.currentDevice = null;
        this.stopEventStream();
        this.updateEventConfigUI();
        this.showLoginOverlay(false);
        this.showToast('Logged out', 'success');
    }

    // Theme Management
    initTheme() {
        const savedTheme = localStorage.getItem('theme') || 'dark';
        this.setTheme(savedTheme);
    }

    setTheme(theme) {
        document.documentElement.setAttribute('data-theme', theme);
        localStorage.setItem('theme', theme);
        this.updateThemeToggle(theme);
    }

    updateThemeToggle(theme) {
        const themeToggle = document.getElementById('themeToggle');
        if (themeToggle) {
            const icon = themeToggle.querySelector('.theme-icon');
            const text = themeToggle.querySelector('.theme-text');
            if (theme === 'dark') {
                icon.textContent = '🌙';
                text.textContent = 'Dark';
            } else {
                icon.textContent = '☀️';
                text.textContent = 'Light';
            }
        }
    }

    toggleTheme() {
        const currentTheme = document.documentElement.getAttribute('data-theme') || 'dark';
        const newTheme = currentTheme === 'dark' ? 'light' : 'dark';
        this.setTheme(newTheme);
    }

    bindElements() {
        // Login elements
        this.loginForm = document.getElementById('loginForm');
        this.logoutBtn = document.getElementById('logoutBtn');
        
        // Header buttons
        this.discoverBtn = document.getElementById('discoverBtn');
        this.addDeviceBtn = document.getElementById('addDeviceBtn');
        this.manageUsersBtn = document.getElementById('manageUsersBtn');
        this.themeToggle = document.getElementById('themeToggle');
        
        // Device list
        this.deviceList = document.getElementById('deviceList');
        this.deviceCount = document.getElementById('deviceCount');
        
        // Panes
        this.welcomePane = document.getElementById('welcomePane');
        this.devicePane = document.getElementById('devicePane');
        
        // Device info
        this.deviceName = document.getElementById('deviceName');
        this.deviceEndpoint = document.getElementById('deviceEndpoint');
        
        // Device actions
        this.refreshDeviceBtn = document.getElementById('refreshDeviceBtn');
        this.deleteDeviceBtn = document.getElementById('deleteDeviceBtn');
        
        // Tabs
        this.tabBtns = document.querySelectorAll('.tab-btn');
        
        // Stream tab
        this.profileSelect = document.getElementById('profileSelect');
        this.startStreamBtn = document.getElementById('startStreamBtn');
        this.stopStreamBtn = document.getElementById('stopStreamBtn');
        this.snapshotBtn = document.getElementById('snapshotBtn');
        this.videoPlayer = document.getElementById('videoPlayer');
        this.snapshotImg = document.getElementById('snapshotImg');
        this.streamStatus = document.getElementById('streamStatus');
        
        // Info tab
        this.infoManufacturer = document.getElementById('infoManufacturer');
        this.infoModel = document.getElementById('infoModel');
        this.infoFirmware = document.getElementById('infoFirmware');
        this.infoSerial = document.getElementById('infoSerial');
        this.infoHardware = document.getElementById('infoHardware');
        this.profileList = document.getElementById('profileList');
        
        // PTZ tab
        this.ptzBtns = document.querySelectorAll('.ptz-btn');
        this.presetList = document.getElementById('presetList');
        
        // Add Device Modal
        this.addDeviceModal = document.getElementById('addDeviceModal');
        this.addDeviceForm = document.getElementById('addDeviceForm');
        this.closeAddModal = document.getElementById('closeAddModal');
        this.cancelAddDevice = document.getElementById('cancelAddDevice');
        
        // Discover Modal
        this.discoverModal = document.getElementById('discoverModal');
        this.discoverStatus = document.getElementById('discoverStatus');
        this.discoveredDevices = document.getElementById('discoveredDevices');
        this.closeDiscoverModal = document.getElementById('closeDiscoverModal');
        this.closeDiscoverBtn = document.getElementById('closeDiscoverBtn');
        this.addSelectedBtn = document.getElementById('addSelectedBtn');
        
        // Credential Modal
        this.credentialModal = document.getElementById('credentialModal');
        this.credentialForm = document.getElementById('credentialForm');
        this.closeCredentialModal = document.getElementById('closeCredentialModal');
        this.cancelCredential = document.getElementById('cancelCredential');
        this.testCredentialBtn = document.getElementById('testCredentialBtn');
        this.credentialStatus = document.getElementById('credentialStatus');
        this.credentialUsername = document.getElementById('credentialUsername');
        this.credentialPassword = document.getElementById('credentialPassword');
        
        // Config tab
        this.configCredentialsForm = document.getElementById('configCredentialsForm');
        this.publicAccessForm = document.getElementById('publicAccessForm');
        this.notificationSettingsForm = document.getElementById('notificationSettingsForm');
        this.configIsPublic = document.getElementById('configIsPublic');
        this.configAllowPublicPTZ = document.getElementById('configAllowPublicPTZ');
        this.publicUrlSection = document.getElementById('publicUrlSection');
        this.configPublicUrl = document.getElementById('configPublicUrl');
        this.copyPublicUrlBtn = document.getElementById('copyPublicUrlBtn');
        this.openPublicUrlBtn = document.getElementById('openPublicUrlBtn');
        this.eventConfigSection = document.getElementById('eventConfigSection');
        this.configEnableEvents = document.getElementById('configEnableEvents');
        this.configEnableBrowserNotifications = document.getElementById('configEnableBrowserNotifications');
        this.configNotificationStatus = document.getElementById('configNotificationStatus');
        this.configEventInterval = document.getElementById('configEventInterval');

        // Snapshots tab
        this.refreshSnapshotsBtn = document.getElementById('refreshSnapshotsBtn');
        
        // Manage Users Modal
        this.manageUsersModal = document.getElementById('manageUsersModal');
        this.closeManageUsersModal = document.getElementById('closeManageUsersModal');
        this.closeManageUsersBtn = document.getElementById('closeManageUsersBtn');
        this.userList = document.getElementById('userList');
        this.addUserBtn = document.getElementById('addUserBtn');
        this.userDetailsSection = document.getElementById('userDetailsSection');
        this.userDetailsTitle = document.getElementById('userDetailsTitle');
        this.userDetailsForm = document.getElementById('userDetailsForm');
        this.userUsername = document.getElementById('userUsername');
        this.userPassword = document.getElementById('userPassword');
        this.userIsAdmin = document.getElementById('userIsAdmin');
        this.userAdminAccessNote = document.getElementById('userAdminAccessNote');
        this.userCameraGroup = document.getElementById('userCameraGroup');
        this.userCameras = document.getElementById('userCameras');
        this.userPTZGroup = document.getElementById('userPTZGroup');
        this.userPTZAllowed = document.getElementById('userPTZAllowed');
        this.cancelUserEdit = document.getElementById('cancelUserEdit');
        this.deleteUserBtn = document.getElementById('deleteUserBtn');
        
        // Toast
        this.toastContainer = document.getElementById('toastContainer');

        // Lightbox
        this.lightboxOverlay = document.getElementById('lightboxOverlay');
        this.lightboxImage = document.getElementById('lightboxImage');
        this.lightboxCaption = document.getElementById('lightboxCaption');
        this.lightboxClose = document.getElementById('lightboxClose');
    }

    bindEvents() {
        // Login form
        if (this.loginForm) {
            this.loginForm.addEventListener('submit', (e) => this.handleLogin(e));
        }
        
        // Logout button
        if (this.logoutBtn) {
            this.logoutBtn.addEventListener('click', () => this.handleLogout());
        }
        
        // Theme toggle
        if (this.themeToggle) {
            this.themeToggle.addEventListener('click', () => this.toggleTheme());
        }
        
        // Header buttons
        this.discoverBtn.addEventListener('click', () => this.discoverDevices());
        this.addDeviceBtn.addEventListener('click', () => this.showAddDeviceModal());
        if (this.manageUsersBtn) {
            this.manageUsersBtn.addEventListener('click', () => this.showManageUsersModal());
        }
        
        // Device actions
        this.refreshDeviceBtn.addEventListener('click', () => this.refreshDevice());
        this.deleteDeviceBtn.addEventListener('click', () => this.deleteDevice());
        
        // Tabs
        this.tabBtns.forEach(btn => {
            btn.addEventListener('click', (e) => this.switchTab(e.target.dataset.tab));
        });
        
        // Stream controls
        this.startStreamBtn.addEventListener('click', () => this.startStream());
        this.stopStreamBtn.addEventListener('click', () => this.stopStream());
        this.snapshotBtn.addEventListener('click', () => this.takeSnapshot());
        
        // PTZ controls
        this.ptzBtns.forEach(btn => {
            btn.addEventListener('mousedown', (e) => this.startPTZMove(e.target.dataset.direction));
            btn.addEventListener('mouseup', () => this.stopPTZMove());
            btn.addEventListener('mouseleave', () => this.stopPTZMove());
            btn.addEventListener('touchstart', (e) => {
                e.preventDefault();
                this.startPTZMove(e.target.dataset.direction);
            });
            btn.addEventListener('touchend', () => this.stopPTZMove());
        });
        
        // Add Device Modal
        this.closeAddModal.addEventListener('click', () => this.hideAddDeviceModal());
        this.cancelAddDevice.addEventListener('click', () => this.hideAddDeviceModal());
        this.addDeviceForm.addEventListener('submit', (e) => this.submitAddDevice(e));
        
        // Discover Modal
        this.closeDiscoverModal.addEventListener('click', () => this.hideDiscoverModal());
        this.closeDiscoverBtn.addEventListener('click', () => this.hideDiscoverModal());
        this.addSelectedBtn.addEventListener('click', () => this.addSelectedDiscoveredDevices());
        
        // Credential Modal
        if (this.closeCredentialModal) {
            this.closeCredentialModal.addEventListener('click', () => this.hideCredentialModal(false));
        }
        if (this.cancelCredential) {
            this.cancelCredential.addEventListener('click', () => this.hideCredentialModal(false));
        }
        if (this.credentialForm) {
            this.credentialForm.addEventListener('submit', (e) => {
                e.preventDefault();
                this.hideCredentialModal(true);
            });
        }
        if (this.testCredentialBtn) {
            this.testCredentialBtn.addEventListener('click', () => this.testCredentials());
        }
        if (this.credentialModal) {
            this.credentialModal.addEventListener('click', (e) => {
                if (e.target === this.credentialModal) this.hideCredentialModal(false);
            });
        }
        
        // Config tab - Credentials form
        if (this.configCredentialsForm) {
            this.configCredentialsForm.addEventListener('submit', (e) => this.submitConfigCredentials(e));
        }
        
        // Config tab - Public access form
        if (this.publicAccessForm) {
            this.publicAccessForm.addEventListener('submit', (e) => this.submitPublicAccessForm(e));
        }
        if (this.notificationSettingsForm) {
            this.notificationSettingsForm.addEventListener('submit', (e) => this.submitNotificationSettingsForm(e));
        }
        if (this.configIsPublic) {
            this.configIsPublic.addEventListener('change', () => this.updatePublicUrlVisibility());
        }
        if (this.copyPublicUrlBtn) {
            this.copyPublicUrlBtn.addEventListener('click', () => this.copyPublicUrl());
        }
        if (this.openPublicUrlBtn) {
            this.openPublicUrlBtn.addEventListener('click', () => this.openPublicUrl());
        }
        if (this.configEnableBrowserNotifications) {
            this.configEnableBrowserNotifications.addEventListener('click', () => this.requestBrowserNotifications());
        }

        // Snapshots tab
        if (this.refreshSnapshotsBtn) {
            this.refreshSnapshotsBtn.addEventListener('click', () => this.loadSnapshots());
        }
        
        // Manage Users Modal
        if (this.closeManageUsersModal) {
            this.closeManageUsersModal.addEventListener('click', () => this.hideManageUsersModal());
        }
        if (this.closeManageUsersBtn) {
            this.closeManageUsersBtn.addEventListener('click', () => this.hideManageUsersModal());
        }
        if (this.manageUsersModal) {
            this.manageUsersModal.addEventListener('click', (e) => {
                if (e.target === this.manageUsersModal) this.hideManageUsersModal();
            });
        }
        if (this.addUserBtn) {
            this.addUserBtn.addEventListener('click', () => this.showNewUserForm());
        }
        if (this.cancelUserEdit) {
            this.cancelUserEdit.addEventListener('click', () => this.hideUserDetails());
        }
        if (this.deleteUserBtn) {
            this.deleteUserBtn.addEventListener('click', () => this.deleteSelectedUser());
        }
        if (this.userDetailsForm) {
            this.userDetailsForm.addEventListener('submit', (e) => this.submitUserForm(e));
        }
        if (this.userIsAdmin) {
            this.userIsAdmin.addEventListener('change', () => this.updateUserAccessControls());
        }
        
        // Close modals on backdrop click
        this.addDeviceModal.addEventListener('click', (e) => {
            if (e.target === this.addDeviceModal) this.hideAddDeviceModal();
        });
        this.discoverModal.addEventListener('click', (e) => {
            if (e.target === this.discoverModal) this.hideDiscoverModal();
        });

        // Lightbox events
        if (this.lightboxClose) {
            this.lightboxClose.addEventListener('click', () => this.closeLightbox());
        }
        if (this.lightboxOverlay) {
            this.lightboxOverlay.addEventListener('click', (e) => {
                if (e.target === this.lightboxOverlay) this.closeLightbox();
            });
        }
        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape' && this.lightboxOverlay && !this.lightboxOverlay.classList.contains('hidden')) {
                this.closeLightbox();
            }
        });
    }

    // API Methods
    async apiRequest(method, url, data = null) {
        const options = {
            method,
            headers: {
                'Content-Type': 'application/json'
            }
        };
        
        if (data) {
            options.body = JSON.stringify(data);
        }
        
        const response = await fetch(url, options);
        
        if (!response.ok) {
            let errorMessage = 'Request failed';
            
            try {
                const text = await response.text();
                
                // Try to parse as JSON first
                try {
                    const json = JSON.parse(text);
                    errorMessage = json.error || json.message || json.Error || json.Message || errorMessage;
                } catch {
                    // If not JSON, check for SOAP XML error
                    if (text.includes('SOAP-ENV') || text.includes('soap:Envelope') || text.includes('<Fault>')) {
                        errorMessage = this.extractSoapError(text);
                    } else if (text.length < 200) {
                        // Short text response, use it as message
                        errorMessage = text || response.statusText;
                    } else {
                        errorMessage = response.statusText || 'Request failed';
                    }
                }
            } catch {
                errorMessage = response.statusText || 'Request failed';
            }
            
            throw new Error(errorMessage);
        }
        
        if (response.status === 204) return null;
        return response.json();
    }

    // Extract human-readable error from SOAP XML
    extractSoapError(xml) {
        // Verify this is actually SOAP XML before extracting
        if (!xml.includes('<') || !xml.includes('>')) {
            return 'ONVIF operation failed';
        }
        
        // Try to extract the text from SOAP-ENV:Text or similar elements
        const patterns = [
            /<SOAP-ENV:Text[^>]*>([^<]+)<\/SOAP-ENV:Text>/i,
            /<faultstring[^>]*>([^<]+)<\/faultstring>/i,
            /<ter:Text[^>]*>([^<]+)<\/ter:Text>/i,
            /<Text[^>]*>([^<]+)<\/Text>/i,
            /<Reason[^>]*>([^<]+)<\/Reason>/i
        ];
        
        for (const pattern of patterns) {
            const match = xml.match(pattern);
            if (match && match[1]) {
                return match[1].trim();
            }
        }
        
        // Check for common ONVIF error codes in SOAP fault elements
        // More specific matching to avoid false positives
        const faultPattern = /<(?:SOAP-ENV:)?Fault[^>]*>[\s\S]*?<\/(?:SOAP-ENV:)?Fault>/i;
        const faultMatch = xml.match(faultPattern);
        const faultContent = faultMatch ? faultMatch[0] : xml;
        
        if (/ter:InvalidArgVal|InvalidArgumentValue/i.test(faultContent)) {
            return 'Invalid argument value';
        }
        if (/Token does not exist|ter:NoToken/i.test(faultContent)) {
            return 'Token does not exist';
        }
        if (/ter:NotAuthorized|NotAuthorized/i.test(faultContent)) {
            return 'Not authorized';
        }
        if (/ter:ActionNotSupported|ActionNotSupported/i.test(faultContent)) {
            return 'Action not supported';
        }
        
        return 'ONVIF operation failed';
    }

    // Map PTZ errors to user-friendly messages
    mapPTZError(errorMessage) {
        const lowerError = errorMessage.toLowerCase();
        
        if (lowerError.includes('not supported') || 
            lowerError.includes('action not supported') ||
            lowerError.includes('actionnotsupported')) {
            return 'Camera does not support this PTZ operation';
        }
        if (lowerError.includes('not authorized') || lowerError.includes('unauthorized')) {
            return 'Not authorized to control PTZ';
        }
        if (lowerError.includes('token does not exist')) {
            return 'PTZ profile token not found';
        }
        if (lowerError.includes('invalidargval') || lowerError.includes('invalid argument')) {
            return 'Invalid PTZ parameter value';
        }
        
        return errorMessage;
    }

    // Map preset errors to user-friendly messages
    mapPresetError(errorMessage) {
        const lowerError = errorMessage.toLowerCase();
        
        if (lowerError.includes('token does not exist') || 
            lowerError.includes('invalidargval') ||
            lowerError.includes('no such preset')) {
            return 'Preset does not exist on camera';
        }
        if (lowerError.includes('not authorized') || lowerError.includes('unauthorized')) {
            return 'Not authorized to use presets';
        }
        if (lowerError.includes('not supported') || lowerError.includes('actionnotsupported')) {
            return 'Camera does not support presets';
        }
        
        return errorMessage;
    }

    // Device Management
    async loadDevices() {
        try {
            this.devices = await this.apiRequest('GET', '/api/devices');
            this.renderDeviceList();
        } catch (error) {
            console.error('Failed to load devices:', error);
            this.showToast('Failed to load devices', 'error');
        }
    }

    renderDeviceList() {
        this.deviceCount.textContent = this.devices.length;
        
        if (this.devices.length === 0) {
            this.deviceList.innerHTML = '<p class="no-devices">No devices added yet</p>';
            return;
        }
        
        this.deviceList.innerHTML = this.devices.map(device => `
            <div class="device-item ${this.currentDevice?.id === device.id ? 'active' : ''}" data-id="${device.id}">
                <span class="device-icon">📹</span>
                <div class="device-details">
                    <div class="device-name">${this.escapeHtml(device.name)}</div>
                    <div class="device-address">${this.escapeHtml(device.endpoint)}</div>
                </div>
                <span class="status-dot ${device.connected ? '' : 'offline'}"></span>
            </div>
        `).join('');
        
        // Bind click events
        this.deviceList.querySelectorAll('.device-item').forEach(item => {
            item.addEventListener('click', () => this.selectDevice(item.dataset.id));
        });
    }

    selectDevice(deviceId) {
        this.currentDevice = this.devices.find(d => d.id === deviceId);
        
        if (!this.currentDevice) return;
        
        // Stop any current stream
        this.stopStream();
        
        // Update UI
        this.welcomePane.classList.add('hidden');
        this.devicePane.classList.remove('hidden');
        
        this.deviceName.textContent = this.currentDevice.name;
        this.deviceEndpoint.textContent = this.currentDevice.endpoint;
        
        // Update info tab
        if (this.currentDevice.info) {
            this.infoManufacturer.textContent = this.currentDevice.info.manufacturer || '-';
            this.infoModel.textContent = this.currentDevice.info.model || '-';
            this.infoFirmware.textContent = this.currentDevice.info.firmwareVersion || '-';
            this.infoSerial.textContent = this.currentDevice.info.serialNumber || '-';
            this.infoHardware.textContent = this.currentDevice.info.hardwareId || '-';
        }
        
        // Update profile list
        this.profileSelect.innerHTML = '';
        this.profileList.innerHTML = '';
        
        if (this.currentDevice.profiles && this.currentDevice.profiles.length > 0) {
            this.currentDevice.profiles.forEach(profile => {
                const option = document.createElement('option');
                option.value = profile.token;
                option.textContent = profile.name || profile.token;
                this.profileSelect.appendChild(option);
                
                const li = document.createElement('li');
                li.textContent = `${profile.name || 'Unnamed'} (${profile.token})`;
                this.profileList.appendChild(li);
            });
        }
        
        // Load PTZ presets
        this.loadPresets();
        
        // Update public access settings
        if (this.configIsPublic) {
            this.configIsPublic.checked = this.currentDevice.isPublic || false;
        }
        if (this.configAllowPublicPTZ) {
            this.configAllowPublicPTZ.checked = this.currentDevice.allowPublicPTZ || false;
        }
        this.updatePublicUrlVisibility();
        this.updateEventConfigUI();
        this.syncEventStreamState();
        
        // Re-render device list to update active state
        this.renderDeviceList();
    }

    updatePublicUrlVisibility() {
        if (!this.publicUrlSection || !this.configPublicUrl || !this.currentDevice) return;
        
        if (this.configIsPublic && this.configIsPublic.checked) {
            this.publicUrlSection.classList.remove('hidden');
            const publicUrl = `${window.location.origin}/public/camera.html?id=${this.currentDevice.id}`;
            this.configPublicUrl.value = publicUrl;
        } else {
            this.publicUrlSection.classList.add('hidden');
        }
    }

    updateEventConfigUI() {
        if (!this.eventConfigSection) return;

        if (!this.currentDevice || !this.currentDevice.supportsEvents) {
            this.eventConfigSection.classList.add('hidden');
            this.stopEventStream();
            return;
        }

        this.eventConfigSection.classList.remove('hidden');
        if (this.configEnableEvents) {
            this.configEnableEvents.checked = !!this.currentDevice.eventsEnabled;
            this.configEnableEvents.disabled = !this.isAdmin;
        }
        if (this.configEventInterval) {
            const interval = Math.max(1, this.currentDevice?.eventIntervalSeconds || 1);
            this.configEventInterval.value = interval;
            this.configEventInterval.disabled = !this.isAdmin;
        }

        this.updateNotificationStatus();
    }

    updateNotificationStatus() {
        if (!this.configNotificationStatus) return;

        if (typeof Notification === 'undefined') {
            this.configNotificationStatus.textContent = 'Browser notifications are not supported in this browser.';
            if (this.configEnableBrowserNotifications) {
                this.configEnableBrowserNotifications.classList.add('hidden');
            }
            return;
        }

        const permission = Notification.permission;
        if (permission === 'granted') {
            this.configNotificationStatus.textContent = 'Browser notifications are enabled.';
            if (this.configEnableBrowserNotifications) {
                this.configEnableBrowserNotifications.classList.add('hidden');
            }
        } else if (permission === 'denied') {
            this.configNotificationStatus.textContent = 'Notifications are blocked in browser settings. Please allow them to receive alerts.';
            if (this.configEnableBrowserNotifications) {
                this.configEnableBrowserNotifications.classList.add('hidden');
            }
        } else {
            this.configNotificationStatus.textContent = 'Click the button to allow browser notifications for camera events.';
            if (this.configEnableBrowserNotifications) {
                this.configEnableBrowserNotifications.classList.remove('hidden');
            }
        }
    }

    async requestBrowserNotifications() {
        if (typeof Notification === 'undefined') {
            this.showToast('Browser notifications are not supported.', 'error');
            return;
        }
        try {
            const result = await Notification.requestPermission();
            if (result !== 'granted') {
                this.showToast('Notifications permission was not granted.', 'error');
            }
        } catch (error) {
            console.error('Notification permission request failed:', error);
        }
        this.updateNotificationStatus();
        this.syncEventStreamState();
    }

    syncEventStreamState() {
        if (!this.currentDevice) {
            this.stopEventStream();
            return;
        }

        const shouldStream = this.currentDevice.supportsEvents && this.currentDevice.eventsEnabled && typeof Notification !== 'undefined' && Notification.permission === 'granted' && typeof EventSource !== 'undefined';
        if (!shouldStream) {
            this.stopEventStream();
            return;
        }

        if (this.eventSource && this.activeEventSourceDevice === this.currentDevice.id) {
            return;
        }

        this.stopEventStream();
        this.startEventStream();
    }

    startEventStream() {
        if (!this.currentDevice || typeof EventSource === 'undefined') return;
        if (this.eventSource) return;
        if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;

        const url = `/api/devices/${this.currentDevice.id}/events/stream`;
        this.eventSource = new EventSource(url);
        this.activeEventSourceDevice = this.currentDevice.id;

        this.eventSource.onmessage = (event) => {
            try {
                const data = JSON.parse(event.data);
                this.pushBrowserNotification(data);
            } catch (error) {
                console.warn('Failed to parse event payload', error);
            }
        };
        this.eventSource.onerror = () => {
            console.warn('Event stream connection issue for device', this.currentDevice?.name);
        };
    }

    stopEventStream() {
        if (this.eventSource) {
            this.eventSource.close();
            this.eventSource = null;
            this.activeEventSourceDevice = null;
        }
    }

    pushBrowserNotification(event) {
        if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;

        const title = `${event.deviceName || 'Camera'} event`;
        const message = event.message || event.topic || 'Camera notification';

        try {
            new Notification(title, {
                body: message,
                tag: `${event.deviceId || ''}-${event.timestamp || Date.now()}`,
                data: event
            });
        } catch (error) {
            console.error('Failed to display browser notification:', error);
        }
    }

    copyPublicUrl() {
        if (this.configPublicUrl) {
            navigator.clipboard.writeText(this.configPublicUrl.value).then(() => {
                this.showToast('URL copied to clipboard', 'success');
            }).catch(() => {
                this.showToast('Failed to copy URL', 'error');
            });
        }
    }

    openPublicUrl() {
        if (this.configPublicUrl && this.configPublicUrl.value) {
            window.open(this.configPublicUrl.value, '_blank');
        }
    }

    async submitPublicAccessForm(e) {
        e.preventDefault();
        
        if (!this.currentDevice) {
            this.showToast('No device selected', 'error');
            return;
        }
        if (!this.isAdmin) {
            this.showToast('Only administrators can change these settings', 'error');
            return;
        }
        
        const isPublic = this.configIsPublic?.checked || false;
        const allowPublicPTZ = this.configAllowPublicPTZ?.checked || false;

        try {
            const payload = { isPublic, allowPublicPTZ };
            const device = await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/config`, payload);
            
            // Update local device
            const index = this.devices.findIndex(d => d.id === device.id);
            if (index !== -1) {
                this.devices[index] = device;
            }
            this.currentDevice = device;
            
            this.updatePublicUrlVisibility();
            this.updateEventConfigUI();
            this.syncEventStreamState();
            this.showToast('Public settings saved', 'success');
        } catch (error) {
            this.showToast('Failed to save public settings: ' + error.message, 'error');
        }
    }

    async submitNotificationSettingsForm(e) {
        e.preventDefault();
        
        if (!this.currentDevice) {
            this.showToast('No device selected', 'error');
            return;
        }
        if (!this.isAdmin) {
            this.showToast('Only administrators can change these settings', 'error');
            return;
        }

        const eventsEnabled = this.configEnableEvents?.checked || false;
        const eventIntervalSeconds = Math.max(1, parseInt(this.configEventInterval?.value, 10) || 1);

        try {
            const payload = { eventsEnabled, eventIntervalSeconds };
            const device = await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/config`, payload);

            // Update local device
            const index = this.devices.findIndex(d => d.id === device.id);
            if (index !== -1) {
                this.devices[index] = device;
            }
            this.currentDevice = device;

            this.updateEventConfigUI();
            this.syncEventStreamState();
            this.showToast('Notification settings saved', 'success');
        } catch (error) {
            this.showToast('Failed to save notification settings: ' + error.message, 'error');
        }
    }

    async refreshDevice() {
        if (!this.currentDevice) return;
        
        try {
            const device = await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/refresh`);
            
            // Update device in list
            const index = this.devices.findIndex(d => d.id === device.id);
            if (index !== -1) {
                this.devices[index] = device;
            }
            
            this.selectDevice(device.id);
            this.showToast('Device refreshed', 'success');
        } catch (error) {
            this.showToast('Failed to refresh device: ' + error.message, 'error');
        }
    }

    async deleteDevice() {
        if (!this.currentDevice) return;
        
        if (!confirm(`Are you sure you want to delete "${this.currentDevice.name}"?`)) {
            return;
        }
        
        try {
            await this.apiRequest('DELETE', `/api/devices/${this.currentDevice.id}`);
            
            this.devices = this.devices.filter(d => d.id !== this.currentDevice.id);
            this.stopEventStream();
            this.currentDevice = null;
            this.updateEventConfigUI();
            
            this.welcomePane.classList.remove('hidden');
            this.devicePane.classList.add('hidden');
            
            this.renderDeviceList();
            this.showToast('Device deleted', 'success');
        } catch (error) {
            this.showToast('Failed to delete device: ' + error.message, 'error');
        }
    }

    // Tabs
    switchTab(tabName) {
        this.tabBtns.forEach(btn => {
            btn.classList.toggle('active', btn.dataset.tab === tabName);
        });
        
        document.querySelectorAll('.tab-pane').forEach(pane => {
            pane.classList.toggle('active', pane.id === tabName + 'Tab');
            pane.classList.toggle('hidden', pane.id !== tabName + 'Tab');
        });

        if (tabName === 'snapshots') {
            this.loadSnapshots();
        }
    }

    // Streaming
    async startStream() {
        if (!this.currentDevice) return;
        
        const profileToken = this.profileSelect.value;
        if (!profileToken) {
            this.showToast('Please select a profile', 'error');
            return;
        }

        this.setStreamStatus('Connecting...');
        this.startStreamBtn.disabled = true;
        
        try {
            // Create peer connection
            this.peerConnection = new RTCPeerConnection({
                iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
            });
            
            // Handle incoming tracks
            this.peerConnection.ontrack = (event) => {
                this.videoPlayer.srcObject = event.streams[0];
                this.snapshotImg.classList.add('hidden');
                this.videoPlayer.classList.remove('hidden');
                this.setStreamStatus('Streaming');

                // Auto-update thumbnail when video starts playing
                this.videoPlayer.onplaying = () => {
                    setTimeout(() => {
                        this.updateThumbnailFromVideo();
                    }, 2000); // Wait 2s for video to stabilize
                };
            };
            
            // Handle ICE connection state
            this.peerConnection.oniceconnectionstatechange = () => {
                if (this.peerConnection) {
                    this.setStreamStatus(`ICE: ${this.peerConnection.iceConnectionState}`);
                    
                    if (this.peerConnection.iceConnectionState === 'failed' ||
                        this.peerConnection.iceConnectionState === 'disconnected') {
                        this.stopStream();
                    }
                }
            };
            
            // Add transceiver for receiving video
            this.peerConnection.addTransceiver('video', { direction: 'recvonly' });
            
            // Create offer
            const offer = await this.peerConnection.createOffer();
            await this.peerConnection.setLocalDescription(offer);
            
            // Wait for ICE gathering
            await new Promise(resolve => {
                if (this.peerConnection.iceGatheringState === 'complete') {
                    resolve();
                } else {
                    this.peerConnection.addEventListener('icegatheringstatechange', () => {
                        if (this.peerConnection.iceGatheringState === 'complete') {
                            resolve();
                        }
                    });
                }
            });
            
            // Send offer to server
            const response = await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/webrtc`, {
                offer: this.peerConnection.localDescription.sdp,
                profileToken: profileToken
            });
            
            // Set remote description
            await this.peerConnection.setRemoteDescription({
                type: 'answer',
                sdp: response.answer
            });
            
            this.stopStreamBtn.disabled = false;
        } catch (error) {
            console.error('Stream error:', error);
            this.setStreamStatus('Error: ' + error.message);
            this.stopStream();
            this.showToast('Failed to start stream: ' + error.message, 'error');
        }
        
        this.startStreamBtn.disabled = false;
    }

    stopStream() {
        if (this.peerConnection) {
            this.peerConnection.close();
            this.peerConnection = null;
        }
        
        this.videoPlayer.srcObject = null;
        this.setStreamStatus('Ready to stream');
        this.stopStreamBtn.disabled = true;
    }

    setStreamStatus(status) {
        this.streamStatus.querySelector('.status-text').textContent = status;
    }

    async takeSnapshot() {
        if (!this.currentDevice) return;
        
        const profileToken = this.profileSelect.value;
        if (!profileToken) {
            this.showToast('Please select a profile', 'error');
            return;
        }

        // Helper to capture from video stream and upload
        const captureFromVideo = async () => {
            if (this.videoPlayer && this.videoPlayer.srcObject && !this.videoPlayer.paused) {
                try {
                    const canvas = document.createElement('canvas');
                    canvas.width = this.videoPlayer.videoWidth;
                    canvas.height = this.videoPlayer.videoHeight;
                    const ctx = canvas.getContext('2d');
                    ctx.drawImage(this.videoPlayer, 0, 0, canvas.width, canvas.height);
                    
                    const dataUrl = canvas.toDataURL('image/jpeg');
                    
                    // Upload to server
                    await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/snapshots`, {
                        image: dataUrl,
                        profileToken: profileToken
                    });
                    
                    this.showToast('Snapshot saved to gallery', 'success');
                    return true;
                } catch (e) {
                    console.error('Fallback snapshot failed:', e);
                    return false;
                }
            }
            return false;
        };
        
        try {
            // Try to capture from video first if playing (faster and reliable)
            if (this.videoPlayer && this.videoPlayer.srcObject && !this.videoPlayer.paused) {
                if (await captureFromVideo()) {
                    return;
                }
            }

            // Fallback to backend capture
            await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/snapshots`, {
                profileToken: profileToken
            });
            this.showToast('Snapshot saved to gallery', 'success');
            
        } catch (error) {
            console.error('Snapshot failed:', error);
            this.showToast('Failed to take snapshot: ' + error.message, 'error');
        }
    }

    async loadSnapshots() {
        if (!this.currentDevice) return;
        
        const list = document.getElementById('snapshotList');
        list.innerHTML = '<div class="loading">Loading snapshots...</div>';
        
        try {
            const snapshots = await this.apiRequest('GET', `/api/devices/${this.currentDevice.id}/snapshots`);
            this.renderSnapshots(snapshots);
        } catch (error) {
            console.error('Failed to load snapshots:', error);
            list.innerHTML = `<div class="error">Failed to load snapshots: ${error.message}</div>`;
        }
    }

    openLightbox(imageUrl, caption) {
        if (!this.lightboxOverlay) return;
        
        this.lightboxImage.src = imageUrl;
        this.lightboxImage.alt = caption;
        this.lightboxCaption.textContent = caption;
        this.lightboxOverlay.classList.remove('hidden');
        document.body.style.overflow = 'hidden'; // Prevent scrolling
    }

    closeLightbox() {
        if (!this.lightboxOverlay) return;
        
        this.lightboxOverlay.classList.add('hidden');
        this.lightboxImage.src = '';
        document.body.style.overflow = ''; // Restore scrolling
    }

    renderSnapshots(snapshots) {
        const list = document.getElementById('snapshotList');
        
        if (!snapshots || snapshots.length === 0) {
            list.innerHTML = '<div class="no-data">No snapshots found</div>';
            return;
        }
        
        // Sort by date desc
        snapshots.sort((a, b) => new Date(b.timestamp) - new Date(a.timestamp));
        
        list.innerHTML = snapshots.map(snap => {
            const date = new Date(snap.timestamp).toLocaleString();
            // Escape single quotes for the onclick handler
            const safePath = snap.path.replace(/'/g, "\\'");
            const safeName = snap.name.replace(/'/g, "\\'");
            
            return `
                <div class="snapshot-item">
                    <div class="snapshot-preview">
                        <img src="${snap.path}" alt="${snap.name}" loading="lazy" onclick="app.openLightbox('${safePath}', '${safeName}')" style="cursor: pointer;">
                    </div>
                    <div class="snapshot-info">
                        <div class="snapshot-meta">
                            <span class="snapshot-user">👤 ${this.escapeHtml(snap.user)}</span>
                            <span class="snapshot-date">🕒 ${date}</span>
                        </div>
                        <div class="snapshot-actions">
                            <a href="${snap.path}" download="${snap.name}" class="btn btn-sm btn-secondary">Download</a>
                            <button class="btn btn-sm btn-danger" onclick="app.deleteSnapshot('${snap.name}')">Delete</button>
                        </div>
                    </div>
                </div>
            `;
        }).join('');
    }

    async deleteSnapshot(filename) {
        if (!confirm('Are you sure you want to delete this snapshot?')) return;
        
        try {
            await this.apiRequest('DELETE', `/api/devices/${this.currentDevice.id}/snapshots/${filename}`);
            this.showToast('Snapshot deleted', 'success');
            this.loadSnapshots();
        } catch (error) {
            console.error('Failed to delete snapshot:', error);
            this.showToast('Failed to delete snapshot: ' + error.message, 'error');
        }
    }

    // PTZ Control
    async startPTZMove(direction) {
        if (!this.currentDevice) return;
        
        const profileToken = this.profileSelect.value;
        let pan = 0, tilt = 0, zoom = 0;
        
        switch (direction) {
            case 'up': tilt = 0.5; break;
            case 'down': tilt = -0.5; break;
            case 'left': pan = -0.5; break;
            case 'right': pan = 0.5; break;
            case 'zoomIn': zoom = 0.5; break;
            case 'zoomOut': zoom = -0.5; break;
            case 'home':
                // Go to home preset if available
                return;
        }
        
        try {
            await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/ptz/move`, {
                profileToken,
                pan,
                tilt,
                zoom
            });
        } catch (error) {
            console.error('PTZ move error:', error);
            const friendlyMessage = this.mapPTZError(error.message);
            this.showToast(friendlyMessage, 'error');
        }
    }

    async stopPTZMove() {
        if (!this.currentDevice) return;
        
        const profileToken = this.profileSelect.value;
        
        try {
            await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/ptz/stop?profileToken=${profileToken}`);
        } catch (error) {
            console.error('PTZ stop error:', error);
            const friendlyMessage = this.mapPTZError(error.message);
            this.showToast(friendlyMessage, 'error');
        }
    }

    async loadPresets() {
        if (!this.currentDevice) return;
        
        const profileToken = this.profileSelect.value;
        if (!profileToken) return;
        
        try {
            const presets = await this.apiRequest('GET', `/api/devices/${this.currentDevice.id}/ptz/presets?profileToken=${profileToken}`);
            
            if (!presets || presets.length === 0) {
                this.presetList.innerHTML = '<p class="no-presets">No presets available</p>';
                return;
            }
            
            this.presetList.innerHTML = presets.map(preset => `
                <button class="preset-btn" data-token="${this.escapeHtml(preset.token)}">
                    ${this.escapeHtml(preset.name || preset.token)}
                </button>
            `).join('');
            
            this.presetList.querySelectorAll('.preset-btn').forEach(btn => {
                btn.addEventListener('click', () => this.gotoPreset(btn.dataset.token));
            });
        } catch (error) {
            this.presetList.innerHTML = '<p class="no-presets">Failed to load presets</p>';
        }
    }

    async gotoPreset(presetToken) {
        if (!this.currentDevice) return;
        
        const profileToken = this.profileSelect.value;
        
        try {
            await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/ptz/goto`, {
                profileToken,
                presetToken
            });
        } catch (error) {
            const friendlyMessage = this.mapPresetError(error.message);
            this.showToast('Failed to go to preset: ' + friendlyMessage, 'error');
        }
    }

    // Add Device Modal
    showAddDeviceModal() {
        this.addDeviceForm.reset();
        this.addDeviceModal.classList.remove('hidden');
    }

    hideAddDeviceModal() {
        this.addDeviceModal.classList.add('hidden');
    }

    async submitAddDevice(e) {
        e.preventDefault();
        
        const name = document.getElementById('deviceNameInput').value;
        const endpoint = document.getElementById('deviceEndpointInput').value;
        const username = document.getElementById('deviceUsernameInput').value;
        const password = document.getElementById('devicePasswordInput').value;
        
        try {
            const device = await this.apiRequest('POST', '/api/devices', {
                name,
                endpoint,
                username,
                password
            });
            
            this.devices.push(device);
            this.renderDeviceList();
            this.hideAddDeviceModal();
            this.selectDevice(device.id);
            this.showToast('Device added successfully', 'success');
        } catch (error) {
            this.showToast('Failed to add device: ' + error.message, 'error');
        }
    }

    // Credential Modal
    showCredentialModal() {
        return new Promise((resolve, reject) => {
            this.credentialResolve = resolve;
            this.credentialReject = reject;
            
            // Reset form
            if (this.credentialUsername) this.credentialUsername.value = '';
            if (this.credentialPassword) this.credentialPassword.value = '';
            if (this.credentialStatus) {
                this.credentialStatus.textContent = '';
                this.credentialStatus.className = 'credential-status hidden';
            }
            
            this.credentialModal.classList.remove('hidden');
        });
    }

    hideCredentialModal(confirmed) {
        this.credentialModal.classList.add('hidden');
        
        if (confirmed && this.credentialResolve) {
            this.credentialResolve({
                username: this.credentialUsername?.value || '',
                password: this.credentialPassword?.value || ''
            });
        } else if (this.credentialReject) {
            this.credentialReject(new Error('Cancelled'));
        }
        
        this.credentialResolve = null;
        this.credentialReject = null;
    }

    async testCredentials() {
        if (!this.credentialStatus) return;
        
        const username = this.credentialUsername?.value || '';
        const password = this.credentialPassword?.value || '';
        
        // Get the first selected device for testing
        const selectedIndex = Array.from(this.selectedDiscoveredDevices)[0];
        if (selectedIndex === undefined || !this.discoveredDevicesList) {
            this.credentialStatus.textContent = 'No device selected for testing';
            this.credentialStatus.className = 'credential-status error';
            return;
        }
        
        const device = this.discoveredDevicesList[selectedIndex];
        
        this.credentialStatus.textContent = 'Testing connection...';
        this.credentialStatus.className = 'credential-status testing';
        
        try {
            // Try to test credentials against the backend
            // This endpoint may not exist, so we handle that gracefully
            await this.apiRequest('POST', '/api/credentials/test', {
                endpoint: device.endpoint,
                username,
                password
            });
            
            this.credentialStatus.textContent = 'Connection successful!';
            this.credentialStatus.className = 'credential-status success';
        } catch (error) {
            // Check if it's a 404 (endpoint doesn't exist)
            if (error.message.includes('404') || error.message.includes('Not Found')) {
                this.credentialStatus.textContent = 'Test endpoint not available. Credentials will be tested when adding device.';
                this.credentialStatus.className = 'credential-status testing';
            } else {
                this.credentialStatus.textContent = 'Connection failed: ' + error.message;
                this.credentialStatus.className = 'credential-status error';
            }
        }
    }

    // Config Tab - Update Credentials
    async submitConfigCredentials(e) {
        e.preventDefault();
        
        if (!this.currentDevice) {
            this.showToast('No device selected', 'error');
            return;
        }
        
        const username = document.getElementById('configUsername')?.value || '';
        const password = document.getElementById('configPassword')?.value || '';
        
        try {
            const device = await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/config`, {
                username,
                password
            });
            
            // Update local device
            const index = this.devices.findIndex(d => d.id === device.id);
            if (index !== -1) {
                this.devices[index] = device;
            }
            this.currentDevice = device;
            
            this.showToast('Credentials updated successfully', 'success');
        } catch (error) {
            this.showToast('Failed to update credentials: ' + error.message, 'error');
        }
    }

    // Discover Modal
    async discoverDevices() {
        this.discoverModal.classList.remove('hidden');
        this.discoverStatus.classList.remove('hidden');
        this.discoveredDevices.classList.add('hidden');
        this.selectedDiscoveredDevices.clear();
        this.addSelectedBtn.disabled = true;
        
        try {
            const devices = await this.apiRequest('POST', '/api/discover?timeout=5s');
            
            this.discoverStatus.classList.add('hidden');
            this.discoveredDevices.classList.remove('hidden');
            
            if (!devices || devices.length === 0) {
                this.discoveredDevices.innerHTML = '<p class="no-devices">No devices found</p>';
                return;
            }
            
            this.discoveredDevices.innerHTML = devices.map((device, index) => `
                <div class="discovered-device" data-index="${index}">
                    <input type="checkbox" id="discover-${index}">
                    <div class="device-details">
                        <div class="device-name">${this.escapeHtml(device.name)}</div>
                        <div class="device-address">${this.escapeHtml(device.endpoint)}</div>
                    </div>
                </div>
            `).join('');
            
            // Store discovered devices
            this.discoveredDevicesList = devices;
            
            // Bind click events
            this.discoveredDevices.querySelectorAll('.discovered-device').forEach((item, index) => {
                item.addEventListener('click', () => {
                    const checkbox = item.querySelector('input[type="checkbox"]');
                    checkbox.checked = !checkbox.checked;
                    item.classList.toggle('selected', checkbox.checked);
                    
                    if (checkbox.checked) {
                        this.selectedDiscoveredDevices.add(index);
                    } else {
                        this.selectedDiscoveredDevices.delete(index);
                    }
                    
                    this.addSelectedBtn.disabled = this.selectedDiscoveredDevices.size === 0;
                });
            });
        } catch (error) {
            this.discoverStatus.innerHTML = `<p>Failed to discover devices: ${this.escapeHtml(error.message)}</p>`;
        }
    }

    hideDiscoverModal() {
        this.discoverModal.classList.add('hidden');
    }

    async addSelectedDiscoveredDevices() {
        let username = '';
        let password = '';
        
        // Show credential modal instead of prompt
        try {
            const credentials = await this.showCredentialModal();
            username = credentials.username;
            password = credentials.password;
        } catch (error) {
            // User cancelled
            return;
        }
        
        let addedCount = 0;
        
        for (const index of this.selectedDiscoveredDevices) {
            const device = this.discoveredDevicesList[index];
            
            try {
                const newDevice = await this.apiRequest('POST', '/api/devices', {
                    name: device.name,
                    endpoint: device.endpoint,
                    username,
                    password
                });
                
                this.devices.push(newDevice);
                addedCount++;
            } catch (error) {
                console.error('Failed to add device:', device.endpoint, error);
                this.showToast(`Failed to add ${device.name}: ${error.message}`, 'error');
            }
        }
        
        this.renderDeviceList();
        this.hideDiscoverModal();
        
        if (addedCount > 0) {
            this.showToast(`Added ${addedCount} device(s)`, 'success');
        }
    }

    // Toast Notifications
    showToast(message, type = 'info') {
        const toast = document.createElement('div');
        toast.className = `toast ${type}`;
        toast.innerHTML = `
            <span class="toast-icon">${type === 'success' ? '✓' : type === 'error' ? '✗' : 'ℹ'}</span>
            <span class="toast-message">${this.escapeHtml(message)}</span>
        `;
        
        this.toastContainer.appendChild(toast);
        
        setTimeout(() => {
            toast.remove();
        }, 5000);
    }

    // Utility
    escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    // User Management
    async showManageUsersModal() {
        this.manageUsersModal.classList.remove('hidden');
        this.hideUserDetails();
        await this.loadUsers();
    }

    hideManageUsersModal() {
        this.manageUsersModal.classList.add('hidden');
    }

    async loadUsers() {
        try {
            this.users = await this.apiRequest('GET', '/api/users');
            this.renderUserList();
        } catch (error) {
            console.error('Failed to load users:', error);
            this.userList.innerHTML = '<p class="no-users">Failed to load users</p>';
        }
    }

    renderUserList() {
        if (this.users.length === 0) {
            this.userList.innerHTML = '<p class="no-users">No users found</p>';
            return;
        }

        this.userList.innerHTML = this.users.map(user => `
            <div class="user-item ${this.selectedUser?.username === user.username ? 'active' : ''}" data-username="${this.escapeHtml(user.username)}">
                <span class="user-name">${this.escapeHtml(user.username)}</span>
                ${user.isAdmin ? '<span class="admin-badge">Admin</span>' : ''}
            </div>
        `).join('');

        // Bind click events
        this.userList.querySelectorAll('.user-item').forEach(item => {
            item.addEventListener('click', () => this.selectUser(item.dataset.username));
        });
    }

    selectUser(username) {
        this.selectedUser = this.users.find(u => u.username === username);
        if (!this.selectedUser) return;

        this.isNewUser = false;
        this.showUserDetails();
    }

    showNewUserForm() {
        this.selectedUser = null;
        this.isNewUser = true;
        this.showUserDetails();
    }

    showUserDetails() {
        this.userDetailsSection.classList.remove('hidden');
        this.userDetailsTitle.textContent = this.isNewUser ? 'New User' : 'Edit User';
        
        // Populate form
        if (this.isNewUser) {
            this.userUsername.value = '';
            this.userUsername.disabled = false;
            this.userPassword.value = '';
            this.userPassword.placeholder = 'Enter password';
            if (this.userIsAdmin) {
                this.userIsAdmin.checked = false;
                this.userIsAdmin.disabled = false;
            }
            this.deleteUserBtn.classList.add('hidden');
        } else {
            this.userUsername.value = this.selectedUser.username;
            this.userUsername.disabled = true;
            this.userPassword.value = '';
            this.userPassword.placeholder = 'Leave blank to keep current';
            if (this.userIsAdmin) {
                this.userIsAdmin.checked = this.selectedUser.isAdmin;
                this.userIsAdmin.disabled = !!this.selectedUser.isDefaultAdmin;
            }
            if (this.selectedUser.isDefaultAdmin) {
                this.deleteUserBtn.classList.add('hidden');
            } else {
                this.deleteUserBtn.classList.remove('hidden');
            }
        }

        if (this.userAdminAccessNote && this.selectedUser?.isDefaultAdmin) {
            this.userAdminAccessNote.textContent = 'The primary administrator automatically has access to every camera and PTZ control.';
        } else if (this.userAdminAccessNote) {
            this.userAdminAccessNote.textContent = 'Administrators automatically have access to all cameras and PTZ controls.';
        }

        this.updateUserAccessControls();

        // Populate camera checkboxes
        this.renderCameraCheckboxes();
        this.renderUserList();
    }

    hideUserDetails() {
        this.userDetailsSection.classList.add('hidden');
        this.selectedUser = null;
        this.isNewUser = false;
        this.renderUserList();
    }

    updateUserAccessControls() {
        const isAdminChecked = this.userIsAdmin?.checked || false;

        if (this.userCameraGroup) {
            this.userCameraGroup.classList.toggle('hidden', isAdminChecked);
        }
        if (this.userPTZGroup) {
            this.userPTZGroup.classList.toggle('hidden', isAdminChecked);
        }
        if (this.userAdminAccessNote) {
            this.userAdminAccessNote.classList.toggle('hidden', !isAdminChecked);
        }
    }

    renderCameraCheckboxes() {
        const selectedCameras = this.selectedUser?.cameras || [];
        const selectedPTZ = this.selectedUser?.ptzAllowed || [];

        if (this.devices.length === 0) {
            this.userCameras.innerHTML = '<p class="text-muted">No cameras available</p>';
            this.userPTZAllowed.innerHTML = '<p class="text-muted">No cameras available</p>';
            return;
        }

        this.userCameras.innerHTML = this.devices.map(device => `
            <label>
                <input type="checkbox" name="cameras" value="${this.escapeHtml(device.id)}" 
                    ${selectedCameras.includes(device.id) ? 'checked' : ''}>
                ${this.escapeHtml(device.name)}
            </label>
        `).join('');

        this.userPTZAllowed.innerHTML = this.devices.map(device => `
            <label>
                <input type="checkbox" name="ptzAllowed" value="${this.escapeHtml(device.id)}"
                    ${selectedPTZ.includes(device.id) ? 'checked' : ''}>
                ${this.escapeHtml(device.name)}
            </label>
        `).join('');
    }

    async submitUserForm(e) {
        e.preventDefault();

        const username = this.userUsername.value.trim();
        const password = this.userPassword.value;
        let isAdmin = this.userIsAdmin?.checked || false;
        if (!this.isNewUser && this.userIsAdmin?.disabled && this.selectedUser) {
            isAdmin = !!this.selectedUser.isAdmin;
        }

        let cameras = [];
        let ptzAllowed = [];
        if (!isAdmin) {
            cameras = Array.from(this.userCameras.querySelectorAll('input[name="cameras"]:checked'))
                .map(cb => cb.value);
            ptzAllowed = Array.from(this.userPTZAllowed.querySelectorAll('input[name="ptzAllowed"]:checked'))
                .map(cb => cb.value);
        }

        if (!username) {
            this.showToast('Username is required', 'error');
            return;
        }

        if (this.isNewUser && !password) {
            this.showToast('Password is required for new users', 'error');
            return;
        }

        try {
            if (this.isNewUser) {
                await this.apiRequest('POST', '/api/users', {
                    username,
                    password,
                    isAdmin,
                    cameras,
                    ptzAllowed
                });
                this.showToast('User created successfully', 'success');
            } else {
                const updateData = {
                    isAdmin,
                    cameras,
                    ptzAllowed
                };
                if (password) {
                    updateData.password = password;
                }
                await this.apiRequest('PUT', `/api/users/${encodeURIComponent(username)}`, updateData);
                this.showToast('User updated successfully', 'success');
            }

            await this.loadUsers();
            this.hideUserDetails();
        } catch (error) {
            this.showToast('Failed to save user: ' + error.message, 'error');
        }
    }

    async deleteSelectedUser() {
        if (!this.selectedUser) return;

        if (!confirm(`Are you sure you want to delete user "${this.selectedUser.username}"?`)) {
            return;
        }

        try {
            await this.apiRequest('DELETE', `/api/users/${encodeURIComponent(this.selectedUser.username)}`);
            this.showToast('User deleted successfully', 'success');
            await this.loadUsers();
            this.hideUserDetails();
        } catch (error) {
            this.showToast('Failed to delete user: ' + error.message, 'error');
        }
    }

    async updateThumbnailFromVideo() {
        if (!this.videoPlayer || this.videoPlayer.paused || this.videoPlayer.ended || !this.currentDevice) return;
        
        try {
            const canvas = document.createElement('canvas');
            // Use a reasonable size for thumbnails
            canvas.width = 320;
            canvas.height = 180;
            const ctx = canvas.getContext('2d');
            ctx.drawImage(this.videoPlayer, 0, 0, canvas.width, canvas.height);
            
            const dataUrl = canvas.toDataURL('image/jpeg', 0.7);
            
            await this.apiRequest('POST', `/api/devices/${this.currentDevice.id}/thumbnail`, {
                image: dataUrl
            });
            console.log('Thumbnail updated from video stream');
        } catch (e) {
            console.warn('Failed to update thumbnail from video', e);
        }
    }
}

// Initialize app
document.addEventListener('DOMContentLoaded', () => {
    window.app = new ONVIFDeviceManager();
});
