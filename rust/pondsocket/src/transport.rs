use async_trait::async_trait;
use pondsocket_common::PondAssigns;
use serde_json::Value;
use tokio::sync::{Mutex, RwLock, mpsc};

use crate::errors::Result;
use crate::types::{Event, TransportType};

#[async_trait]
pub trait Transport: Send + Sync {
    fn id(&self) -> &str;
    async fn send_event(&self, event: Event) -> Result<()>;
    async fn close(&self) -> Result<()>;
    fn transport_type(&self) -> TransportType;
    async fn is_active(&self) -> bool;
    async fn get_assign(&self, key: &str) -> Option<Value>;
    async fn set_assign(&self, key: &str, value: Value);
    async fn clone_assigns(&self) -> PondAssigns;
}

pub struct MemoryTransport {
    id: String,
    assigns: RwLock<PondAssigns>,
    active: RwLock<bool>,
    tx: mpsc::Sender<Event>,
    rx: Mutex<mpsc::Receiver<Event>>,
}

impl MemoryTransport {
    pub fn new(id: impl Into<String>, assigns: PondAssigns) -> Self {
        let (tx, rx) = mpsc::channel(1024);
        Self {
            id: id.into(),
            assigns: RwLock::new(assigns),
            active: RwLock::new(true),
            tx,
            rx: Mutex::new(rx),
        }
    }

    pub async fn recv(&self) -> Option<Event> {
        self.rx.lock().await.recv().await
    }
}

#[async_trait]
impl Transport for MemoryTransport {
    fn id(&self) -> &str {
        &self.id
    }

    async fn send_event(&self, event: Event) -> Result<()> {
        let _ = self.tx.send(event).await;
        Ok(())
    }

    async fn close(&self) -> Result<()> {
        *self.active.write().await = false;
        Ok(())
    }

    fn transport_type(&self) -> TransportType {
        TransportType::InMemory
    }

    async fn is_active(&self) -> bool {
        *self.active.read().await
    }

    async fn get_assign(&self, key: &str) -> Option<Value> {
        self.assigns.read().await.get(key).cloned()
    }

    async fn set_assign(&self, key: &str, value: Value) {
        self.assigns.write().await.insert(key.to_owned(), value);
    }

    async fn clone_assigns(&self) -> PondAssigns {
        self.assigns.read().await.clone()
    }
}
