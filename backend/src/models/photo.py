"""
数据库模型定义
"""
from sqlalchemy import Column, Integer, String, Text, Boolean, DateTime, ForeignKey, Table
from sqlalchemy.orm import declarative_base, relationship
from sqlalchemy.sql import func
from datetime import datetime

Base = declarative_base()

# 照片-标签关联表
photo_tags = Table(
    'photo_tags',
    Base.metadata,
    Column('photo_id', Integer, ForeignKey('photos.id', ondelete='CASCADE'), primary_key=True),
    Column('tag_id', Integer, ForeignKey('tags.id', ondelete='CASCADE'), primary_key=True)
)


class Photo(Base):
    """照片模型"""
    __tablename__ = 'photos'

    id = Column(Integer, primary_key=True, autoincrement=True)
    file_path = Column(String, nullable=False, unique=True)
    file_name = Column(String, nullable=False)
    file_size = Column(Integer)
    width = Column(Integer)
    height = Column(Integer)
    taken_at = Column(DateTime)  # 照片拍摄时间
    created_at = Column(DateTime, default=func.now())
    updated_at = Column(DateTime, default=func.now(), onupdate=func.now())

    # AI 分析结果
    description = Column(Text)  # 80-200 字描述
    caption = Column(String)  # 8-30 字文案
    category = Column(String)  # 分类

    # 评分
    art_score = Column(Integer)  # 美观艺术性评分 (0-100)
    memory_score = Column(Integer)  # 值得回忆评分 (0-100)

    # 分析状态
    analyzed = Column(Boolean, default=False)
    analyzed_at = Column(DateTime)

    # 关系
    tags = relationship('Tag', secondary=photo_tags, back_populates='photos')
    display_history = relationship('DisplayHistory', back_populates='photo', cascade='all, delete-orphan')


class Tag(Base):
    """标签模型"""
    __tablename__ = 'tags'

    id = Column(Integer, primary_key=True, autoincrement=True)
    name = Column(String, nullable=False, unique=True)
    created_at = Column(DateTime, default=func.now())

    # 关系
    photos = relationship('Photo', secondary=photo_tags, back_populates='tags')


class DisplayHistory(Base):
    """展示历史模型"""
    __tablename__ = 'display_history'

    id = Column(Integer, primary_key=True, autoincrement=True)
    photo_id = Column(Integer, ForeignKey('photos.id', ondelete='CASCADE'), nullable=False)
    displayed_at = Column(DateTime, default=func.now())
    display_reason = Column(String)  # 展示原因

    # 关系
    photo = relationship('Photo', back_populates='display_history')


class Setting(Base):
    """配置模型"""
    __tablename__ = 'settings'

    key = Column(String, primary_key=True)
    value = Column(Text)
    description = Column(Text)
    updated_at = Column(DateTime, default=func.now(), onupdate=func.now())
