# Tasks - Melhorias de Performance e Escalabilidade

Análise técnica dos gargalos no dispatcher e componentes relacionados, ordenados por prioridade.

---

## 🔴 Alta Prioridade

### 1. HTTP Client Compartilhado

**Problema:** Cada destino HTTP cria seu próprio `http.Client`, resultando em connection pools isolados e não reutilizados.

**Local:** `internal/dispatch/dispatcher.go:127`

**Código atual:**
```go
type HTTPDestination struct {
    client *http.Client  // ← Um client por destino
}

func NewHTTPDestination(...) *HTTPDestination {
    return &HTTPDestination{
        client: &http.Client{Timeout: 30 * time.Second},
    }
}
```

**Impacto:**
- 10 destinos = 10 pools isolados
- `MaxIdleConnsPerHost: 2` (default do Go) por pool
- Conexões TCP não reutilizadas entre destinos do mesmo endpoint
- `TIME_WAIT` acumula no SO
- Latência: ~30ms por nova conexão (TCP handshake)

**Solução:**
```go
type Dispatcher struct {
    httpClient   *http.Client  // ← Compartilhado
    destinations map[string][]Destination
}

func NewDispatcher() *Dispatcher {
    return &Dispatcher{
        httpClient: &http.Client{
            Transport: &http.Transport{
                MaxIdleConns:        100,
                MaxIdleConnsPerHost: 50,
                MaxConnsPerHost:     100,
                IdleConnTimeout:     90 * time.Second,
            },
            Timeout: 30 * time.Second,
        },
        destinations: make(map[string][]Destination),
    }
}

// Passar client compartilhado para HTTPDestination
func NewHTTPDestination(client *http.Client, endpoint string, ...) *HTTPDestination {
    return &HTTPDestination{
        client: client,  // ← Usa client compartilhado
        ...
    }
}
```

**Arquivos para modificar:**
- `internal/dispatch/dispatcher.go`
- `internal/watcher/manager.go` (atualizar chamada `NewHTTPDestination`)

---

### 2. Dispatch Assíncrono

**Problema:** Eventos são enviados sequencialmente para cada destino, acumulando latência.

**Local:** `internal/dispatch/dispatcher.go:48-65`

**Código atual:**
```go
func (d *Dispatcher) Dispatch(ctx context.Context, table string, record streams.StreamRecord) error {
    for _, dest := range dests {
        if err := dest.Send(ctx, record); err != nil {  // ← Bloqueia aqui
            lastErr = err
        }
    }
}
```

**Impacto:**
- 3 destinos × 500ms cada = 1.5s latência total
- MongoDB change stream cursor pode timeout
- Retry queue enche mesmo com baixo volume

**Solução:**
```go
func (d *Dispatcher) Dispatch(ctx context.Context, table string, record streams.StreamRecord) error {
    d.mu.RLock()
    dests, ok := d.destinations[table]
    d.mu.RUnlock()

    if !ok {
        return nil
    }

    var wg sync.WaitGroup
    errChan := make(chan error, len(dests))

    for _, dest := range dests {
        wg.Add(1)
        go func(dest Destination) {
            defer wg.Done()
            if err := dest.Send(ctx, record); err != nil {
                errChan <- err
            }
        }(dest)
    }

    wg.Wait()
    close(errChan)

    var lastErr error
    for err := range errChan {
        lastErr = err
    }
    return lastErr
}
```

**Arquivos para modificar:**
- `internal/dispatch/dispatcher.go`

---

### 3. Idempotência com 2 Redis Calls

**Problema:** Cada evento faz 2 chamadas Redis separadas (EXISTS + SETEX).

**Local:** `internal/watcher/manager.go:230-252`

**Código atual:**
```go
// Check idempotency (1a chamada Redis)
processed, err := m.redisClient.IsProcessed(ctx, eventID)

// ... process event ...

// Mark as processed (2a chamada Redis)
if err := m.redisClient.MarkProcessed(ctx, eventID, 24*time.Hour); err != nil {
    ...
}
```

**Impacto:**
- 2 chamadas Redis por evento processado
- 1000 eventos/s = 2000 calls Redis/s
- Latência: ~2ms por evento só em idempotência (local)

**Solução:**
```go
// Opção A: Lua script atômico
const script = `
if redis.call('EXISTS', KEYS[1]) == 1 then
    return 1
end
redis.call('SETEX', KEYS[1], ARGV[1], '1')
return 0
`

// Opção B: Pipeline Redis
pipe := r.client.Pipeline()
existsCmd := pipe.Exists(ctx, key)
setCmd := pipe.Set(ctx, key, "1", ttl)
pipe.Exec(ctx)
```

**Arquivos para modificar:**
- `internal/redis/client.go` (adicionar novo método)
- `internal/watcher/manager.go` (usar novo método)

---

## 🟡 Média Prioridade

### 4. Marshal JSON por Request

**Problema:** Mesmo evento é marshalado múltiplas vezes se tiver múltiplos destinos.

**Local:** `internal/dispatch/dispatcher.go:145-148`

**Código atual:**
```go
func (h *HTTPDestination) Send(ctx context.Context, record streams.StreamRecord) error {
    data, err := json.Marshal(record)  // ← Marshal por destino
    ...
}
```

**Impacto:**
- 3 destinos = 3× marshal do mesmo JSON
- CPU e alocação de memória desnecessárias

**Solução:**
```go
// Marshal no Dispatch, passa []byte para destinos
func (d *Dispatcher) Dispatch(ctx context.Context, table string, record streams.StreamRecord) error {
    data, err := json.Marshal(record)  // ← Marshal único
    if err != nil {
        return err
    }

    for _, dest := range dests {
        go func(dest Destination) {
            dest.Send(ctx, data)  // ← Recebe JSON pronto
        }(dest)
    }
}

// HTTPDestination recebe []byte
func (h *HTTPDestination) Send(ctx context.Context, data []byte) error {
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoint, bytes.NewReader(data))
    ...
}
```

**Arquivos para modificar:**
- `internal/dispatch/dispatcher.go`
- `internal/dispatch/dispatcher.go` (interface Destination)
- `internal/watcher/manager.go` (handler)

---

### 5. Log em Todo Evento

**Problema:** Log síncrono em cada evento enviado consome I/O desnecessário.

**Local:** `internal/dispatch/dispatcher.go:172`

**Código atual:**
```go
log.Printf("Sent event to HTTP endpoint %s (status: %d)", h.endpoint, resp.StatusCode)
```

**Impacto:**
- 100 eventos/s = 100 writes de log/s
- Pode representar 10-20% do tempo de processamento
- Bloqueia se stdout estiver cheio (Docker)

**Solução:**
```go
// Opção A: Log apenas erros
if resp.StatusCode >= 400 {
    log.Printf("HTTP event failed: %s (status: %d)", h.endpoint, resp.StatusCode)
}

// Opção B: Log sampling
if atomic.AddInt64(&h.eventCount, 1) % 100 == 0 {
    log.Printf("Sent event to HTTP endpoint %s (status: %d)", h.endpoint, resp.StatusCode)
}
```

**Arquivos para modificar:**
- `internal/dispatch/dispatcher.go`

---

### 6. Buffer entre Watcher e Processamento

**Problema:** Eventos são processados sincronamente, podendo causar timeout do cursor MongoDB.

**Local:** `internal/watcher/manager.go:172-174`

**Código atual:**
```go
if err := watcher.Start(ctx, func(record streams.StreamRecord) error {
    return m.handleEvent(ctx, table.TableName, record)  // ← Síncrono
});
```

**Impacto:**
- Se `handleEvent` demora (HTTP lento) → cursor não avança
- MongoDB pode achar que worker travou
- Burst de eventos → processamento fica lento

**Solução:**
```go
// No startWatcher
eventChan := make(chan streams.StreamRecord, 1000)

// Worker assíncrono
go func() {
    for record := range eventChan {
        if err := m.handleEvent(ctx, table.TableName, record); err != nil {
            log.Printf("Handle event failed: %v", err)
        }
    }
}()

// Watcher só joga no channel
if err := watcher.Start(ctx, func(record streams.StreamRecord) error {
    select {
    case eventChan <- record:
        return nil
    default:
        return fmt.Errorf("event channel full")
    }
});
```

**Arquivos para modificar:**
- `internal/watcher/manager.go`

---

## 🟢 Baixa Prioridade

### 7. Retry Processor com Polling

**Problema:** Polling a cada 5 segundos atrasa retries.

**Local:** `internal/retry/processor.go:72-84`

**Código atual:**
```go
func (p *Processor) processLoop(ctx context.Context) {
    ticker := time.NewTicker(p.interval)  // ← 5 segundos
    for {
        select {
        case <-ticker.C:
            p.processQueue(ctx)
        }
    }
}
```

**Impacto:**
- Evento falha → espera até 5s para retry
- Aumenta latência de recuperação

**Solução:**
```go
// Opção A: Reduzir intervalo
interval: 1 * time.Second  // ← Mais responsivo

// Opção B: Blocking ZSET (complexo)
// Usar Redis ZSET com blocking para acordar quando tiver evento pronto
```

**Arquivos para modificar:**
- `internal/retry/processor.go`

---

### 8. Clear Completo de Destinos no Refresh

**Problema:** Sempre remove todos destinos e recria, mesmo se só mudou 1 campo.

**Local:** `internal/watcher/manager.go:383-395`

**Código atual:**
```go
// Clear existing destinations
d.Clear(tableName)  // ← Remove TODOS

// Re-register destinations (recria TODOS)
m.registerDestinations(ctx, tableName, table.Destinations)
```

**Impacto:**
- Alocação desnecessária de objetos
- GC tem mais trabalho

**Solução:**
```go
// Diff destinos atuais vs novos
// Só recria o que mudou (endpoint, event_types, bearer_token)
func (m *Manager) refreshDestinations(...) error {
    current := m.getDestinations(tableName)
    desired := table.Destinations

    toAdd, toRemove := diffDestinations(current, desired)

    for _, dest := range toRemove {
        m.dispatcher.Remove(tableName, dest)
    }
    for _, dest := range toAdd {
        m.dispatcher.Register(tableName, newDest(dest))
    }
}
```

**Arquivos para modificar:**
- `internal/dispatch/dispatcher.go` (adicionar método Remove)
- `internal/watcher/manager.go`

---

## Plano de Ação Sugerido

### Fase 1 (Crítico - Fazer Agora)
1. ✅ HTTP Client Compartilhado
2. ✅ Dispatch Assíncrono

### Fase 2 (Importante - Próxima Sprint)
3. ✅ Idempotência com 1 Redis Call
4. ✅ Buffer entre Watcher e Processamento

### Fase 3 (Otimização - Quando Sobrar Tempo)
5. ✅ Marshal JSON Único
6. ✅ Log Sampling
7. ✅ Retry Polling Mais Rápido
8. ✅ Diff de Destinos no Refresh

---

## Métricas de Sucesso

Após implementar as melhorias:

| Métrica | Antes | Depois (Meta) |
|---------|-------|---------------|
| Latência p99 (1 destino) | 500ms | < 200ms |
| Latência p99 (3 destinos) | 1500ms | < 500ms |
| Conexões TCP ativas | N × destinos | N / reutilizadas |
| Calls Redis/evento | 2 | 1 |
| CPU (marshal) | N × destinos | N (único) |

---

*Gerado em: 2026-05-03*
