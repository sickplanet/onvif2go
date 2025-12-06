//Public Index 
async function loadCameras() {
    const grid = document.getElementById('cameraGrid');
    
    try {
        const response = await fetch('/public/cameras');
        if (!response.ok) {
            throw new Error('Failed to load cameras');
        }
        
        const cameras = await response.json();
        
        if (cameras.length === 0) {
            grid.innerHTML = `
                <div class="no-cameras">
                    <p>No public cameras available</p>
                    <p style="font-size: 1rem;">Check back later or contact the administrator</p>
                </div>
            `;
            return;
        }
        
        grid.innerHTML = cameras.map(camera => `
            <a href="/public/camera.html?id=${encodeURIComponent(camera.id)}" class="camera-link">
                <div class="camera-card">
                    <div class="camera-thumbnail">
                        ${camera.thumbnailUrl 
                            ? `<img src="${camera.thumbnailUrl}" alt="${escapeHtml(camera.name)}" onerror="this.parentElement.innerHTML='<span class=\\'no-thumbnail\\'>📷</span>'">`
                            : '<span class="no-thumbnail">📷</span>'
                        }
                    </div>
                    <div class="camera-info">
                        <h3>${escapeHtml(camera.name)}</h3>
                        <div class="camera-badges">
                            <span class="badge">Public</span>
                            ${camera.allowPtz ? '<span class="badge badge-ptz">PTZ Control</span>' : ''}
                        </div>
                    </div>
                </div>
            </a>
        `).join('');
    } catch (error) {
        console.error('Error loading cameras:', error);
        grid.innerHTML = `
            <div class="no-cameras">
                <p>Failed to load cameras</p>
                <p style="font-size: 1rem;">${escapeHtml(error.message)}</p>
            </div>
        `;
    }
}

function escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// Initialize theme
const savedTheme = localStorage.getItem('theme') || 'dark';
document.documentElement.setAttribute('data-theme', savedTheme);

// Load cameras on page load
document.addEventListener('DOMContentLoaded', loadCameras);