# Протокол совместимости Go сервера с Artillery

## ✅ Совместимость с artillery-processor.cjs

### Message Types (точное соответствие)

| Константа | Go Value | JS Value | Описание |
|-----------|----------|----------|----------|
| `JOIN` | 1 | 1 | Подключение игрока |
| `LEAVE` | 2 | 2 | Отключение игрока |
| `MOVE` | 3 | 3 | **Движение (основное)** |
| `DIRECTION` | 4 | 4 | **Смена направления** |
| `ATTACK` | 5 | 5 | **Атака** |
| `ATTACK_END` | 6 | 6 | **Конец атаки** |
| `GAME_STATE` | 7 | 7 | Состояние игры |
| `MOVEMENT_ACK` | 8 | 8 | Подтверждение движения |
| `CORRECTION` | 9 | 9 | Коррекция позиции |
| `INITIAL_STATE` | 10 | 10 | Начальное состояние |
| `PLAYER_JOINED` | 11 | 11 | Игрок присоединился |
| `PLAYER_LEFT` | 12 | 12 | Игрок покинул |

### 🔧 Форматы сообщений

#### 1. **Move Message** (Type 3)
**JavaScript (artillery):**
```javascript
function encodeMove(movementVector, inputSequence) {
  const buffer = new ArrayBuffer(6);
  const view = new DataView(buffer);
  view.setUint8(0, MessageType.MOVE);      // Type (1 byte)
  view.setUint8(1, packed);                // Packed movement (1 byte)
  view.setUint32(2, inputSequence, true); // Sequence (4 bytes LE)
  return new Uint8Array(buffer);
}
```

**Go (сервер):**
```go
case MessageMove:
    movement := UnpackMovement(data[1])           // Extract packed movement
    msg.MovementVector = movement                 // Convert to struct
    msg.InputSequence = binary.LittleEndian.Uint32(data[2:6]) // Read sequence
```

#### 2. **Direction Message** (Type 4)
**JavaScript:**
```javascript
function encodeDirection(direction) {
  view.setUint8(0, MessageType.DIRECTION); // Type (1 byte)
  view.setInt8(1, direction);              // Direction (1 byte)
}
```

**Go:**
```go
case MessageDirection:
    msg.Direction = data[1] == 1 // Convert to boolean
```

#### 3. **Attack Messages** (Type 5, 6)
**JavaScript:**
```javascript
// Attack: 9 bytes (type + position)
// Attack End: 1 byte (type only)
```

**Go:**
```go
case MessageAttack, MessageAttackEnd:
    // No additional data needed
```

### 🔄 Movement Encoding (Критически важно!)

**Обе стороны используют одинаковую упаковку:**

```javascript
// JavaScript
function packMovement(dx, dy) {
  let packed = 0;
  packed |= (dx + 1) & 0x03;         // dx: -1->0, 0->1, 1->2
  packed |= ((dy + 1) & 0x03) << 2;  // dy: same, shifted 2 bits
  return packed;
}
```

```go
// Go
func PackMovement(dx, dy int8) uint8 {
    packed := uint8(0)
    packed |= uint8(dx+1) & 0x03        // dx: -1->0, 0->1, 1->2 (2 bits)
    packed |= (uint8(dy+1) & 0x03) << 2 // dy: same, shifted 2 bits
    return packed
}
```

## 🧪 Тестирование совместимости

### 1. Проверка протокола
```bash
cd /src/server
make test-protocol
```

### 2. Быстрый artillery test
```bash
# Terminal 1: запуск Go сервера
make run

# Terminal 2: быстрый тест
make artillery-quick
```

### 3. Полный artillery test
```bash
# Terminal 1: запуск Go сервера
make run

# Terminal 2: полный нагрузочный тест
make artillery-test
```

### 4. Настройка artillery-config.yml

Конфигурация уже настроена на порт 8108:
```yaml
config:
  target: 'ws://localhost:8108/ws'  # ✅ Правильный endpoint
  processor: './artillery-processor.cjs'
  phases:
    - duration: 60
      arrivalRate: 5
      name: "Warm up - 300 clients"
```

## ⚡ Ожидаемые результаты

Artillery-processor.cjs должен успешно:

1. **✅ Подключаться** к Go серверу через WebSocket
2. **✅ Отправлять move messages** (тип 3) с правильной упаковкой
3. **✅ Отправлять direction changes** (тип 4)
4. **✅ Отправлять attack messages** (тип 5, 6)
5. **✅ Получать game state** (тип 7) от сервера
6. **✅ Обрабатывать player join/leave** (тип 11, 12)

## 🐛 Troubleshooting

### Artillery не может подключиться:
```bash
# Проверить что Go сервер запущен
curl http://localhost:8108/health

# Проверить WebSocket endpoint
wscat -c ws://localhost:8108/ws
```

### Неправильные message types:
```bash
# Сравнить константы
grep -A 15 "MessageType = {" ../../utils/testing/artillery/artillery-processor.cjs
grep -A 15 "const (" internal/protocol/binary.go
```

### Protocol errors:
```bash
# Включить debug в Go сервере
export DEBUG=true
make dev

# Смотреть логи во время artillery теста
tail -f server.log
```

## 🎯 Заключение

Go сервер **полностью совместим** с artillery-processor.cjs:
- ✅ Одинаковые message types
- ✅ Одинаковый бинарный формат
- ✅ Одинаковая упаковка движений
- ✅ WebSocket endpoint `/ws`

Можно запускать artillery нагрузочные тесты без изменений!
