//Public Camera
//State
let camera = null;
let peerConnection = null;
let profileToken = null;

// Elements
const loadingState = document.getElementById('loadingState');
const errorState = document.getElementById('errorState');
const errorMessage = document.getElementById('errorMessage');
const cameraView = document.getElementById('cameraView');
const cameraName = document.getElementById('cameraName');
const cameraId = document.getElementById('cameraId');
const videoPlayer = document.getElementById('videoPlayer');
const snapshotImg = document.getElementById('snapshotImg');
const streamStatus = document.getElementById('streamStatus');
const startStreamBtn = document.getElementById('startStreamBtn');
const stopStreamBtn = document.getElementById('stopStreamBtn');
const snapshotBtn = document.getElementById('snapshotBtn');
const ptzSection = document.getElementById('ptzSection');

// Initialize theme
const savedTheme = localStorage.getItem('theme') || 'dark';
document.documentElement.setAttribute('data-theme', savedTheme);

// Get camera ID from URL
const urlParams = new URLSearchParams(window.location.search);
const deviceId = urlParams.get('id');

async function loadCamera() {
    if (!deviceId) {
        showError('No camera ID provided');
        return;
    }

    try {
        const response = await fetch(`/public/cameras/${encodeURIComponent(deviceId)}`);
        if (!response.ok) {
            throw new Error('Camera not found or not public');
        }

        camera = await response.json();
        
        // Get profile token from the public camera response
        if (camera.profiles && camera.profiles.length > 0) {
            profileToken = camera.profiles[0].token;
        }

        showCamera();
    } catch (error) {
        console.error('Error loading camera:', error);
        showError(error.message);
    }
}

function showCamera() {
    loadingState.classList.add('hidden');
    errorState.classList.add('hidden');
    cameraView.classList.remove('hidden');

    cameraName.textContent = camera.name;
    cameraId.textContent = `ID: ${camera.id}`;
    document.title = `${camera.name} - ONVIF Device Manager`;

    // Show PTZ controls if allowed
    if (camera.allowPtz) {
        ptzSection.classList.remove('hidden');
        initPTZControls();
    }
}

function showError(message) {
    loadingState.classList.add('hidden');
    cameraView.classList.add('hidden');
    errorState.classList.remove('hidden');
    errorMessage.textContent = message;
}

function setStreamStatus(status) {
    streamStatus.textContent = status;
}

async function startStream() {
    if (!profileToken) {
        setStreamStatus('No profile available');
        return;
    }

    setStreamStatus('Connecting...');
    startStreamBtn.disabled = true;

    try {
        peerConnection = new RTCPeerConnection({
            iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
        });

        peerConnection.ontrack = (event) => {
            videoPlayer.srcObject = event.streams[0];
            snapshotImg.classList.add('hidden');
            videoPlayer.classList.remove('hidden');
            setStreamStatus('Streaming');
        };

        peerConnection.oniceconnectionstatechange = () => {
            if (peerConnection) {
                setStreamStatus(`ICE: ${peerConnection.iceConnectionState}`);
                if (peerConnection.iceConnectionState === 'failed' ||
                    peerConnection.iceConnectionState === 'disconnected') {
                    stopStream();
                }
            }
        };

        peerConnection.addTransceiver('video', { direction: 'recvonly' });

        const offer = await peerConnection.createOffer();
        await peerConnection.setLocalDescription(offer);

        await new Promise(resolve => {
            if (peerConnection.iceGatheringState === 'complete') {
                resolve();
            } else {
                peerConnection.addEventListener('icegatheringstatechange', () => {
                    if (peerConnection.iceGatheringState === 'complete') {
                        resolve();
                    }
                });
            }
        });

        const response = await fetch(`/api/devices/${deviceId}/webrtc`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                offer: peerConnection.localDescription.sdp,
                profileToken: profileToken
            })
        });

        if (!response.ok) {
            throw new Error('Failed to connect to stream');
        }

        const data = await response.json();

        await peerConnection.setRemoteDescription({
            type: 'answer',
            sdp: data.answer
        });

        stopStreamBtn.disabled = false;
    } catch (error) {
        console.error('Stream error:', error);
        setStreamStatus('Error: ' + error.message);
        stopStream();
    }

    startStreamBtn.disabled = false;
}

function stopStream() {
    if (peerConnection) {
        peerConnection.close();
        peerConnection = null;
    }
    videoPlayer.srcObject = null;
    setStreamStatus('Ready to stream');
    stopStreamBtn.disabled = true;
}

async function takeSnapshot() {
    if (!profileToken) return;

    const captureFromVideo = () => {
        if (videoPlayer && videoPlayer.srcObject && !videoPlayer.paused) {
            try {
                const canvas = document.createElement('canvas');
                canvas.width = videoPlayer.videoWidth;
                canvas.height = videoPlayer.videoHeight;
                const ctx = canvas.getContext('2d');
                ctx.drawImage(videoPlayer, 0, 0, canvas.width, canvas.height);
                
                const dataUrl = canvas.toDataURL('image/jpeg');
                snapshotImg.src = dataUrl;
                snapshotImg.classList.remove('hidden');
                videoPlayer.classList.add('hidden');
                return true;
            } catch (e) {
                console.error('Fallback snapshot failed:', e);
            }
        }
        return false;
    };

    try {
        const url = `/api/devices/${deviceId}/snapshot/${profileToken}?t=${Date.now()}`;
        const response = await fetch(url);
        
        if (!response.ok) {
            throw new Error(response.statusText);
        }
        
        const blob = await response.blob();
        const objectUrl = URL.createObjectURL(blob);
        
        if (snapshotImg.src && snapshotImg.src.startsWith('blob:')) {
            URL.revokeObjectURL(snapshotImg.src);
        }
        
        snapshotImg.src = objectUrl;
        snapshotImg.classList.remove('hidden');
        videoPlayer.classList.add('hidden');
    } catch (error) {
        console.warn('Native snapshot failed, trying fallback:', error);
        if (!captureFromVideo()) {
            console.error('Failed to take snapshot:', error);
        }
    }
}

async function sendPTZCommand(direction) {
    if (!camera || !camera.allowPtz) return;

    let pan = 0, tilt = 0, zoom = 0;
    switch (direction) {
        case 'up': tilt = 0.5; break;
        case 'down': tilt = -0.5; break;
        case 'left': pan = -0.5; break;
        case 'right': pan = 0.5; break;
        case 'zoomIn': zoom = 0.5; break;
        case 'zoomOut': zoom = -0.5; break;
        case 'home': return;
    }

    try {
        await fetch(`/api/devices/${deviceId}/ptz/move`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ profileToken, pan, tilt, zoom })
        });
    } catch (error) {
        console.error('PTZ error:', error);
    }
}

async function stopPTZCommand() {
    if (!camera || !camera.allowPtz) return;

    try {
        await fetch(`/api/devices/${deviceId}/ptz/stop?profileToken=${profileToken}`, {
            method: 'POST'
        });
    } catch (error) {
        console.error('PTZ stop error:', error);
    }
}

function initPTZControls() {
    document.querySelectorAll('.ptz-btn').forEach(btn => {
        btn.addEventListener('mousedown', () => sendPTZCommand(btn.dataset.direction));
        btn.addEventListener('mouseup', stopPTZCommand);
        btn.addEventListener('mouseleave', stopPTZCommand);
        btn.addEventListener('touchstart', (e) => {
            e.preventDefault();
            sendPTZCommand(btn.dataset.direction);
        });
        btn.addEventListener('touchend', stopPTZCommand);
    });
}

// Bind events
startStreamBtn.addEventListener('click', startStream);
stopStreamBtn.addEventListener('click', stopStream);
snapshotBtn.addEventListener('click', takeSnapshot);

// Load camera on page load
document.addEventListener('DOMContentLoaded', loadCamera);