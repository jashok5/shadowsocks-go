package obfs

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math"
	"sync"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/binaryx"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/bytesx"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/randomx"

	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/cache"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/log"
)

func conbineToBytes(data ...any) []byte {
	total := 0
	for _, item := range data {
		total += combineItemSize(item)
	}
	out := make([]byte, 0, total)
	for _, item := range data {
		out = appendCombineItem(out, item)
	}
	return out
}

func combineItemSize(item any) int {
	switch v := item.(type) {
	case nil:
		return 0
	case []byte:
		return len(v)
	case string:
		return len(v)
	case byte, int8, bool:
		return 1
	case uint16, int16:
		return 2
	case uint32, int32, float32:
		return 4
	case uint64, int64, float64:
		return 8
	default:
		buf := new(bytes.Buffer)
		if err := binary.Write(buf, binary.BigEndian, item); err != nil {
			return 0
		}
		return buf.Len()
	}
}

func appendCombineItem(dst []byte, item any) []byte {
	switch v := item.(type) {
	case nil:
		return dst
	case []byte:
		return append(dst, v...)
	case string:
		return append(dst, v...)
	case byte:
		return append(dst, v)
	case int8:
		return append(dst, byte(v))
	case bool:
		if v {
			return append(dst, 1)
		}
		return append(dst, 0)
	case uint16:
		tmp := [2]byte{}
		binary.BigEndian.PutUint16(tmp[:], v)
		return append(dst, tmp[:]...)
	case int16:
		tmp := [2]byte{}
		binary.BigEndian.PutUint16(tmp[:], uint16(v))
		return append(dst, tmp[:]...)
	case uint32:
		tmp := [4]byte{}
		binary.BigEndian.PutUint32(tmp[:], v)
		return append(dst, tmp[:]...)
	case int32:
		tmp := [4]byte{}
		binary.BigEndian.PutUint32(tmp[:], uint32(v))
		return append(dst, tmp[:]...)
	case uint64:
		tmp := [8]byte{}
		binary.BigEndian.PutUint64(tmp[:], v)
		return append(dst, tmp[:]...)
	case int64:
		tmp := [8]byte{}
		binary.BigEndian.PutUint64(tmp[:], uint64(v))
		return append(dst, tmp[:]...)
	case float32:
		tmp := [4]byte{}
		binary.BigEndian.PutUint32(tmp[:], math.Float32bits(v))
		return append(dst, tmp[:]...)
	case float64:
		tmp := [8]byte{}
		binary.BigEndian.PutUint64(tmp[:], math.Float64bits(v))
		return append(dst, tmp[:]...)
	default:
		buf := new(bytes.Buffer)
		if err := binary.Write(buf, binary.BigEndian, item); err != nil {
			return dst
		}
		return append(dst, buf.Bytes()...)
	}
}

func MustHexDecode(data string) []byte {
	result, err := hex.DecodeString(data)
	if err != nil {
		return []byte{}
	}
	return result
}

type HashNewFunc func() hash.Hash

func errorReply2048() []byte {
	return bytes.Repeat([]byte{'E'}, 2048)
}

func setRawTrans(rawTrans *bool) {
	*rawTrans = true
}

func setRawTransAndClearRecv(rawTrans *bool, recvBuf *[]byte) {
	*rawTrans = true
	*recvBuf = []byte{}
}

func hmacsha1(key, data []byte) []byte {
	mac := hmac.New(sha1.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func hmacmd5(key, data []byte) []byte {
	mac := hmac.New(md5.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func hashSum(data []byte, h func() hash.Hash) []byte {
	hashInstance := h()
	hashInstance.Write(data)
	return hashInstance.Sum(nil)
}

func hmacSum(key, data []byte, h func() hash.Hash) []byte {
	mac := hmac.New(h, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func matchBegin(str1, str2 []byte) bool {
	if len(str1) >= len(str2) {
		if bytes.Equal(str1[:len(str2)], str2) {
			return true
		}
	}
	return false
}

type AuthBase struct {
	Plain
	Method             string
	NoCompatibleMethod string
	Overhead           int
	RawTrans           bool
}

func NewAuthBase(method string) (*AuthBase, error) {
	newPlain, err := NewPlain(method)
	if err != nil {
		return nil, err
	}
	return &AuthBase{
		Plain:    newPlain,
		Method:   method,
		Overhead: 4,
	}, nil
}

func (authBase *AuthBase) GetOverhead(bool) int {
	return authBase.Overhead
}

func (authBase *AuthBase) NotMatchReturn(buf []byte) ([]byte, bool) {
	authBase.RawTrans = true
	authBase.Overhead = 0
	if authBase.GetMethod() == authBase.NoCompatibleMethod {
		return errorReply2048(), false
	}
	return buf, false
}

type ClientQueue struct {
	mu         sync.Mutex
	Front      int
	Back       int
	Alloc      *sync.Map
	Enable     bool
	LastUpdate time.Time
	Ref        int
}

func NewClientQueue(beginID int) *ClientQueue {
	return &ClientQueue{
		Front:      beginID - 64,
		Back:       beginID + 1,
		Alloc:      new(sync.Map),
		Enable:     true,
		LastUpdate: time.Now(),
		Ref:        0,
	}
}

func (c *ClientQueue) Update() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updateLocked()
}

func (c *ClientQueue) updateLocked() {
	c.LastUpdate = time.Now()
}

func (c *ClientQueue) AddRef() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addRefLocked()
}

func (c *ClientQueue) addRefLocked() {
	c.Ref += 1
}

func (c *ClientQueue) DelRef() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delRefLocked()
}

func (c *ClientQueue) delRefLocked() {
	if c.Ref > 0 {
		c.Ref -= 1
	}
}

func (c *ClientQueue) IsActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isActiveLocked()
}

func (c *ClientQueue) isActiveLocked() bool {
	return c.Ref > 0 && time.Since(c.LastUpdate).Seconds() < 60*10
}

func (c *ClientQueue) ReEnable(connectionID int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reEnableLocked(connectionID)
}

func (c *ClientQueue) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Enable
}

func (c *ClientQueue) reEnableLocked(connectionID int) {
	c.Enable = true
	c.Front = connectionID - 64
	c.Back = connectionID + 1
	c.Alloc = new(sync.Map)
}

func (c *ClientQueue) Insert(connectionID int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.Enable {
		log.WarnwSampled("obfs_auth_not_enable", 10*time.Second, "obfs auth not enable")
		return false
	}
	if !c.isActiveLocked() {
		c.reEnableLocked(connectionID)
	}
	c.updateLocked()
	if connectionID < c.Front {
		log.WarnwSampled("obfs_auth_deprecated_id", 10*time.Second, "obfs auth deprecated id", log.FieldConnectionID, connectionID, "front", c.Front)
		return false
	}
	if connectionID > c.Front+0x4000 {
		log.WarnwSampled("obfs_auth_wrong_id", 10*time.Second, "obfs auth wrong id", log.FieldConnectionID, connectionID, "front", c.Front)
		return false
	}
	if _, ok := c.Alloc.Load(connectionID); ok {
		log.WarnwSampled("obfs_auth_replay", 10*time.Second, "obfs auth replay detected", log.FieldConnectionID, connectionID)
		return false
	}
	if c.Back <= connectionID {
		c.Back = connectionID + 1
	}
	c.Alloc.Store(connectionID, 1)
	for {
		if _, ok := c.Alloc.Load(c.Back); !ok || c.Front+0x1000 >= c.Back {
			break
		}
		if _, ok := c.Alloc.Load(c.Front); ok {
			c.Alloc.Delete(c.Front)
		}
		c.Front += 1
	}
	c.addRefLocked()
	return true
}

type AuthChainData struct {
	mu            sync.RWMutex
	Name          string
	UserID        map[string]*cache.LRU
	LocalClientId []byte
	ConnectionID  int
	MaxClient     int
	MaxBuffer     int
}

func NewObfsAuthChainData(name string) *AuthChainData {
	result := &AuthChainData{
		Name:          name,
		UserID:        make(map[string]*cache.LRU),
		LocalClientId: []byte{},
		ConnectionID:  0,
	}
	result.SetMaxClient(64)
	return result
}

func (o *AuthChainData) Update(userID []byte, clientID, _ int) {
	localClientID := o.getOrCreateUserCache(userID)
	var r *ClientQueue = nil
	if localClientID != nil {
		r, _ = localClientID.Get(clientID).(*ClientQueue)
	}
	if r != nil {
		r.Update()
	}
}

func (o *AuthChainData) SetMaxClient(maxClient int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.MaxClient = maxClient
	o.MaxBuffer = int(math.Max(float64(maxClient), 1024))
}

func (o *AuthChainData) Insert(userID []byte, clientID, connectionID int) bool {
	localClientID := o.getOrCreateUserCache(userID)
	var r, _ = localClientID.Get(clientID).(*ClientQueue)
	if r == nil || !r.Enabled() {
		if localClientID.First() == nil || localClientID.Len() < o.MaxClient {
			log.InfowSampled("obfs_new_client", 10*time.Second, "obfs new client", log.FieldClientID, clientID, log.FieldUserID, binaryx.LEBytesToUInt32(userID))
			if !localClientID.IsExist(clientID) {
				localClientID.Put(clientID, NewClientQueue(connectionID))
			} else {
				localClientID.Get(clientID).(*ClientQueue).ReEnable(connectionID)
			}
			return localClientID.Get(clientID).(*ClientQueue).Insert(connectionID)
		}

		localClientIDFirst := localClientID.First()
		if localClientIDFirst != nil && !localClientID.Get(localClientIDFirst).(*ClientQueue).IsActive() {
			localClientID.Delete(localClientIDFirst)
			if !localClientID.IsExist(clientID) {
				localClientID.Put(clientID, NewClientQueue(connectionID))
			} else {
				localClientID.Get(clientID).(*ClientQueue).ReEnable(connectionID)
			}
			return localClientID.Get(clientID).(*ClientQueue).Insert(connectionID)
		}

		log.WarnwSampled("obfs_no_inactive_client", 10*time.Second, "obfs no inactive client", log.FieldUserID, binaryx.LEBytesToUInt32(userID), log.FieldClientID, clientID, log.FieldName, o.Name)
		return false
	}

	return localClientID.Get(clientID).(*ClientQueue).Insert(connectionID)
}

func (o *AuthChainData) Remove(userID string, clientID int) {
	o.mu.RLock()
	localClientID := o.UserID[userID]
	o.mu.RUnlock()
	if localClientID != nil {
		if localClientID.IsExist(clientID) {
			localClientID.Get(clientID).(*ClientQueue).DelRef()
		}
	}
}

func (o *AuthChainData) AuthData() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	utcTime := uint32(time.Now().Unix() & 0xFFFFFFFF)
	if o.ConnectionID > 0xFF000000 {
		o.LocalClientId = []byte{}
	}
	if len(o.LocalClientId) == 0 {
		o.LocalClientId = randomx.RandomBytes(4)
		o.ConnectionID = int(binaryx.LEBytesToUInt32(randomx.RandomBytes(4)) & 0xFFFFFFFF)
	}
	o.ConnectionID++
	return bytesx.ContactSlice(
		binaryx.LEUint32ToBytes(utcTime),
		o.LocalClientId,
		binaryx.LEUint32ToBytes(uint32(o.ConnectionID)),
	)
}

func (o *AuthChainData) GetConnectionID() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.ConnectionID
}

func (o *AuthChainData) SetConnectionID(connectionID int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ConnectionID = connectionID
}

func (o *AuthChainData) SetClientID(clientID []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.LocalClientId = clientID
}

func (o *AuthChainData) GetClientID() []byte {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.LocalClientId
}

func (o *AuthChainData) getOrCreateUserCache(userID []byte) *cache.LRU {
	key := string(userID)
	o.mu.RLock()
	lru := o.UserID[key]
	o.mu.RUnlock()
	if lru != nil {
		return lru
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	lru = o.UserID[key]
	if lru == nil {
		lru = cache.NewLruCache(60 * time.Second)
		o.UserID[key] = lru
	}
	return lru
}
